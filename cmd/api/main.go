package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"movies/internal/data"
	"movies/internal/mailer"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	_ "github.com/lib/pq"
	"golang.org/x/time/rate"
)

type rateLimitConfig struct {
	rps      float64
	burst    int
	disabled bool
}

type dbConfig struct {
	dsn             string
	maxOpenConns    int
	maxIdleConns    int
	maxIdleLifeTime string
}

type smtp struct {
	host     string
	port     int
	username string
	password string
	sender   string
}

type config struct {
	port    int
	env     string
	db      dbConfig
	limiter rateLimitConfig
	smtp    smtp
}

type application struct {
	config config
	logger *slog.Logger
	models data.Models
	mailer mailer.Mailer
	wg     sync.WaitGroup
}

var (
	version   = "1.0.0"
	buildTime string
)

func customHTTPErrorHandler(err error, c echo.Context) {
	var status int
	var message interface{}

	switch e := err.(type) {
	case *echo.HTTPError:
		status = e.Code
		message = e.Message
	default:
		status = http.StatusInternalServerError
		message = "the server encountered a problem and could not process your request"
	}

	if !c.Response().Committed {
		c.JSON(status, envelope{"error": message})
	}
}

func main() {
	envErr := godotenv.Load()
	if envErr != nil {
		log.Fatal("Error loading .env file")
	}

	displayVersion := flag.Bool("version", false, "Display version and exit")
	flag.Parse()

	if *displayVersion {
		fmt.Printf("Version:\t%s\n", version)
		fmt.Printf("Build time:\t%s\n", buildTime)
		os.Exit(0)
	}

	port := os.Getenv("PORT")
	realPort, _ := strconv.Atoi(port)
	dsn := os.Getenv("DSN")
	rps := os.Getenv("RPS")
	realRps, _ := strconv.ParseFloat(rps, 64)
	burst := os.Getenv("BURST")
	realBurst, _ := strconv.Atoi(burst)
	disabled := os.Getenv("DISABLED")
	realDisabled, _ := strconv.ParseBool(disabled)
	smtpHost := os.Getenv("SMTP_HOST")
	smtpPort := os.Getenv("SMTP_PORT")
	realSmtpPort, _ := strconv.Atoi(smtpPort)
	smtpUsername := os.Getenv("SMTP_USERNAME")
	smtpPassword := os.Getenv("SMTP_PASSWORD")
	smtpSender := os.Getenv("SMTP_SENDER")
	cfg := config{
		port: realPort,
		db: dbConfig{
			dsn:             dsn,
			maxOpenConns:    25,
			maxIdleConns:    25,
			maxIdleLifeTime: "15m",
		},
		limiter: rateLimitConfig{
			rps:      realRps,
			burst:    realBurst,
			disabled: realDisabled,
		},
		smtp: smtp{
			host:     smtpHost,
			port:     realSmtpPort,
			username: smtpUsername,
			password: smtpPassword,
			sender:   smtpSender,
		},
	}
	flag.StringVar(&cfg.env, "env", "development", "Environment(development|staging|production)")
	flag.Parse()

	db, dbErr := openDB(cfg)
	if dbErr != nil {
		log.New(os.Stdout, "", log.Ldate|log.Ltime).Fatal(dbErr)
	}

	defer db.Close()

	e := echo.New()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogStatus:        true,
		LogURI:           true,
		LogError:         true,
		LogMethod:        true,
		LogContentLength: true,
		HandleError:      true,

		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			if v.Error != nil {
				logger.LogAttrs(context.Background(), slog.LevelError, "REQUEST_ERROR",
					slog.String("method", v.Method),
					slog.String("uri", v.URI),
					slog.Int("status", v.Status),
					slog.String("content_length", v.ContentLength),
					slog.String("err", v.Error.Error()),
				)
			} else if v.Error == nil && v.Status == 500 {
				logger.LogAttrs(context.Background(), slog.LevelError, "PANIC",
					slog.String("method", v.Method),
					slog.String("uri", v.URI),
					slog.Int("status", v.Status),
					slog.String("content_length", v.ContentLength),
				)
			} else {
				logger.LogAttrs(context.Background(), slog.LevelInfo, "REQUEST",
					slog.String("method", v.Method),
					slog.String("uri", v.URI),
					slog.Int("status", v.Status),
					slog.String("content_length", v.ContentLength),
				)
			}
			return nil
		},
	}))

	config := middleware.RateLimiterConfig{
		Skipper: func(c echo.Context) bool {
			return cfg.limiter.disabled
		},
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(
			middleware.RateLimiterMemoryStoreConfig{Rate: rate.Limit(cfg.limiter.rps), Burst: cfg.limiter.burst, ExpiresIn: 3 * time.Minute},
		),
		IdentifierExtractor: func(ctx echo.Context) (string, error) {
			id := ctx.RealIP()
			fmt.Println("client IP: ", id)
			return id, nil
		},
		ErrorHandler: func(c echo.Context, err error) error {
			return c.JSON(http.StatusForbidden, "Status forbidden")
		},
		DenyHandler: func(c echo.Context, identifier string, err error) error {
			return c.JSON(http.StatusTooManyRequests, "Too many requests")
		},
	}

	logger.Info("database connection pool established")

	app := &application{
		config: cfg,
		logger: logger,
		models: data.NewModels(db),
		mailer: mailer.New(cfg.smtp.host, cfg.smtp.port, cfg.smtp.username, cfg.smtp.password, cfg.smtp.sender),
	}

	e.Use(echoprometheus.NewMiddleware("myapp"))
	e.GET("/metrics", echoprometheus.NewHandler())

	e.Use(app.CustomRecover())
	e.Use(middleware.RateLimiterWithConfig(config))
	e.Use(middleware.CORS())
	e.Use(app.Authenticate())

	e.HTTPErrorHandler = customHTTPErrorHandler
	app.routes(e)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go func() {
		if err := e.Start(fmt.Sprintf(":%d", cfg.port)); err != nil && err != http.ErrServerClosed {
			e.Logger.Fatal("shutting down the server")
		}
	}()

	<-ctx.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		e.Logger.Fatal(err)
	}
}

func openDB(cfg config) (*sql.DB, error) {
	db, err := sql.Open("postgres", cfg.db.dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(cfg.db.maxOpenConns)
	db.SetMaxIdleConns(cfg.db.maxIdleConns)

	duration, err := time.ParseDuration(cfg.db.maxIdleLifeTime)
	if err != nil {
		return nil, err
	}

	db.SetConnMaxLifetime(duration)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = db.PingContext(ctx)

	if err != nil {
		return nil, err
	}

	return db, err
}
