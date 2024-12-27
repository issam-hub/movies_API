package main

import (
	"errors"
	"fmt"
	"movies/internal/data"
	"movies/internal/validator"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

func (app *application) getMoviesHandler(c echo.Context) error {
	var input struct {
		Title  string
		Genres pq.StringArray
		data.Filter
	}

	v := validator.New()

	input.Title = c.QueryParam("title")
	input.Genres = app.readCSV(c.QueryParams(), "genres", []string{})
	input.Page = app.readInt(c.QueryParams(), "page", 1, v)
	input.PageSize = app.readInt(c.QueryParams(), "page_size", 5, v)
	input.Sort = app.readString(c.QueryParams(), "sort", "id")
	input.SortSafeList = []string{"id", "title", "year", "runtime", "-id", "-title", "-year", "-runtime"}

	if data.ValidateFilters(v, &input.Filter); !v.Valid() {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, v.Errors)
	}

	movies, metaData, err := app.models.Movies.GetAll(input.Title, input.Genres, input.Filter)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, envelope{"message": "Movies returned succussfully", "metadata": metaData, "movies": movies})
}
func (app *application) createMovieHandler(c echo.Context) error {
	var input struct {
		Title   string   `json:"title"`
		Year    int32    `json:"year"`
		Runtime int32    `json:"runtime"`
		Genres  []string `json:"genres"`
	}

	if err := c.Bind(&input); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	movie := &data.Movie{
		Title:   input.Title,
		Year:    input.Year,
		Runtime: input.Runtime,
		Genres:  input.Genres,
	}

	v := validator.New()

	if data.ValidateMovie(v, movie); !v.Valid() {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, v.Errors)
	}

	err := app.models.Movies.Insert(movie)
	if err != nil {
		return err
	}

	c.Response().Header().Set("Location", fmt.Sprintf("/v1/movies/%d", movie.ID))

	return c.JSON(http.StatusCreated, envelope{
		"message": "Movie created successfully",
		"movie":   movie,
	})
}
func (app *application) showMovieHandler(c echo.Context) error {
	id, err := app.readIDParam(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}

	movie, err := app.models.Movies.Get(id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrNoRecordFound):
			return echo.NewHTTPError(http.StatusNotFound, "Movie not found")
		default:
			return err
		}
	}

	return c.JSON(http.StatusOK, envelope{"message": "Movie returned succussfully", "movie": movie})

}

func (app *application) updateMovieHandler(c echo.Context) error {
	id, err := app.readIDParam(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}

	movie, err := app.models.Movies.Get(id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrNoRecordFound):
			return echo.NewHTTPError(http.StatusNotFound, data.ErrNoRecordFound.Error())
		default:
			return err
		}
	}

	var input struct {
		Title   *string  `json:"title,omitempty"`
		Year    *int32   `json:"year,omitempty"`
		Runtime *int32   `json:"runtime,omitempty"`
		Genres  []string `json:"genres,omitempty"`
	}

	if err := c.Bind(&input); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if input.Title != nil {
		movie.Title = *input.Title
	}
	if input.Year != nil {
		movie.Year = *input.Year
	}
	if input.Runtime != nil {
		movie.Runtime = *input.Runtime
	}
	if input.Genres != nil {
		movie.Genres = input.Genres
	}

	v := validator.New()

	if data.ValidateMovie(v, movie); !v.Valid() {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, v.Errors)
	}

	err = app.models.Movies.Update(movie)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
			return echo.NewHTTPError(http.StatusConflict, data.ErrEditConflict.Error())
		default:
			return err
		}
	}

	c.Response().Header().Set("Location", fmt.Sprintf("/v1/movies/%d", movie.ID))

	return c.JSON(http.StatusCreated, envelope{"message": "Movie Updated succussfully", "movie": movie})
}

func (app *application) deleteMovieHandler(c echo.Context) error {
	id, err := app.readIDParam(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}

	err = app.models.Movies.Delete(id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrNoRecordFound):
			return echo.NewHTTPError(http.StatusNotFound, data.ErrNoRecordFound.Error())
		default:
			return err
		}
	}

	return c.JSON(http.StatusOK, envelope{"message": "Movie deleted succussfully"})
}

func (app *application) registerUserHandler(c echo.Context) error {
	var input struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	if err := c.Bind(&input); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	user := &data.User{
		Name:      input.Name,
		Email:     input.Email,
		Activated: false,
	}

	err := user.Password.Set(input.Password)
	if err != nil {
		return err
	}

	v := validator.New()

	if data.ValidateUser(v, user); !v.Valid() {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, v.Errors)
	}

	err = app.models.Users.Insert(user)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrDuplicateEmail):
			v.AddError("email", "a user with this email address already exists")
			return echo.NewHTTPError(http.StatusUnprocessableEntity, v.Errors)
		default:
			return err
		}
	}

	err = app.models.Permissions.AddForUser(user.ID)
	if err != nil {
		return err
	}

	token, err := app.models.Tokens.New(user.ID, 2*24*time.Hour, data.ScopeActivation)
	if err != nil {
		return err
	}

	app.background(func() {
		data := map[string]interface{}{
			"activationToken": token.PlainText,
			"Name":            user.Name,
		}
		err = app.mailer.Send(user.Email, "user_welcome.tmpl", data)
		if err != nil {
			c.Logger().Error(err)
		}
	})

	return c.JSON(http.StatusCreated, envelope{
		"message": "User created successfully",
		"user":    user,
	})
}

func (app *application) activateUserHandler(c echo.Context) error {
	var input struct {
		Token string `json:"token"`
	}

	if err := c.Bind(&input); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	v := validator.New()

	if data.ValidateTokenPlainText(v, input.Token); !v.Valid() {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, v.Errors)
	}

	user, err := app.models.Users.GetByToken(data.ScopeActivation, input.Token)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrNoRecordFound):
			v.AddError("token", "invalid or expired activation token")
			return echo.NewHTTPError(http.StatusUnprocessableEntity, v.Errors)
		default:
			return err
		}
	}

	user.Activated = true

	err = app.models.Users.Update(user)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
			return echo.NewHTTPError(http.StatusConflict, data.ErrEditConflict.Error())
		default:
			return err
		}
	}

	err = app.models.Tokens.DeleteAllForUser(data.ScopeActivation, user.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, envelope{
		"message": "User activated successfully",
		"user":    user,
	})
}

func (app *application) authenticationTokenHandler(c echo.Context) error {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	if err := c.Bind(&input); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	v := validator.New()
	data.ValidateEmail(v, input.Email)
	data.ValidPlainText(v, &input.Password)

	if !v.Valid() {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, v.Errors)
	}

	user, err := app.models.Users.GetByEmail(input.Email)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrNoRecordFound):
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid authentication credentials")
		default:
			return err
		}
	}

	match, err := user.Password.Matches(input.Password)
	if err != nil {
		return err
	}

	if !match {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid authentication credentials")
	}

	token, err := app.models.Tokens.New(user.ID, 1*24*time.Hour, data.ScopeAuth)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, envelope{
		"message":    "Authentication token created successfully",
		"auth_token": token,
	})
}
