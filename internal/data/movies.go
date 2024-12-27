package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"movies/internal/validator"
	"time"

	"github.com/lib/pq"
)

type Movie struct {
	ID        int            `json:"id"`
	Title     string         `json:"title"`
	Year      int32          `json:"year,omitempty"`
	Runtime   int32          `json:"runtime,omitempty"`
	Genres    pq.StringArray `json:"genres,omitempty"`
	CreatedAt time.Time      `json:"-"`
	Version   int32          `json:"version"`
}

func ValidateMovie(v *validator.Validator, movie *Movie) {
	// title validation
	v.Check(movie.Title != "", "title", "title must be provided")
	v.Check(len(movie.Title) <= 500, "title", "title should be less than or equal to 500 characters long")

	// year validation
	v.Check(movie.Year != 0, "year", "year must be provided")
	v.Check(movie.Year >= 1888 && movie.Year <= int32(time.Now().Year()), "year", "year must be between 1888 and the current year")

	// runtime validation
	v.Check(movie.Runtime > 0, "runtime", "runtime must be provided and a positive integer")

	// genres validation
	v.Check(movie.Genres != nil, "genres", "genres must be provided")
	v.Check(len(movie.Genres) >= 1 && len(movie.Genres) <= 5, "genres", "genres most contain at least 1 and no more than 5 items")
	v.Check(validator.Unique(movie.Genres), "genres", "genres must contain unique items")
}

type MovieModel struct {
	DB *sql.DB
}

func (m *MovieModel) GetAll(title string, genres pq.StringArray, filters Filter) ([]*Movie, MetaData, error) {
	offset := (filters.Page - 1) * filters.PageSize

	query := fmt.Sprintf(`SELECT COUNT(*) OVER(), id, created_at, title, year, runtime, genres, version FROM movies 
	WHERE (to_tsvector('simple', title) @@ plainto_tsquery('simple', $1) OR $1='') 
	AND (genres @> $2 OR $2 = '{}') 
	ORDER BY %s %s,id ASC 
	LIMIT $3 OFFSET $4`, filters.sortColumn(), filters.sortDirection())
	movies := []*Movie{}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

	defer cancel()

	args := []interface{}{title, pq.Array(genres), filters.PageSize, offset}

	rows, err := m.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, MetaData{}, err
	}

	totalRecords := 0
	for rows.Next() {
		movie := &Movie{}
		err := rows.Scan(&totalRecords, &movie.ID, &movie.CreatedAt, &movie.Title, &movie.Year, &movie.Runtime, &movie.Genres, &movie.Version)
		if err != nil {
			return nil, MetaData{}, err
		}
		movies = append(movies, movie)
	}
	if err := rows.Err(); err != nil {
		return nil, MetaData{}, err
	}
	metaData := calculateMetadata(totalRecords, filters.Page, filters.PageSize)
	return movies, metaData, nil
}

func (m *MovieModel) Insert(movie *Movie) error {
	query := `INSERT INTO movies (title, year, runtime, genres) VALUES ($1, $2, $3, $4) RETURNING id, created_at, version`
	args := []interface{}{
		movie.Title,
		movie.Year,
		movie.Runtime,
		movie.Genres,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

	defer cancel()
	return m.DB.QueryRowContext(ctx, query, args...).Scan(&movie.ID, &movie.CreatedAt, &movie.Version)
}

func (m *MovieModel) Get(id int) (*Movie, error) {
	if id < 1 {
		return nil, ErrNoRecordFound
	}

	var movie Movie

	query := `SELECT id, created_at, title, year, runtime, genres, version FROM movies WHERE id = $1`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

	defer cancel()

	err := m.DB.QueryRowContext(ctx, query, id).Scan(&movie.ID, &movie.CreatedAt, &movie.Title, &movie.Year, &movie.Runtime, &movie.Genres, &movie.Version)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, ErrNoRecordFound
		default:
			return nil, err
		}
	}
	return &movie, nil
}

func (m *MovieModel) Update(movie *Movie) error {
	query := `UPDATE movies SET title = $1, year = $2, runtime = $3, genres = $4, version = version + 1
	WHERE id = $5 AND version = $6 RETURNING version`

	args := []interface{}{
		movie.Title,
		movie.Year,
		movie.Runtime,
		movie.Genres,
		movie.ID,
		movie.Version,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

	defer cancel()

	err := m.DB.QueryRowContext(ctx, query, args...).Scan(&movie.Version)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return ErrEditConflict
		default:
			return err
		}
	}
	return nil
}

func (m *MovieModel) Delete(id int) error {
	if id < 1 {
		return ErrNoRecordFound
	}

	query := `DELETE FROM movies WHERE id = $1`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

	defer cancel()

	result, err := m.DB.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNoRecordFound
	}
	return nil
}
