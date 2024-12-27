package main

import (
	"errors"
	"movies/internal/data"
	"movies/internal/validator"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

func (app *application) CustomRecover() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			defer func() interface{} {
				if err := recover(); err != nil {
					c.Response().Header().Set("Connection", "close")
					return err
				}
				app.logger.Info("completing background tasks...")
				app.wg.Wait()
				return nil
			}()
			return next(c)
		}
	}
}

// func CustomGlobalRateLimit() echo.MiddlewareFunc {
// 	limiter := rate.NewLimiter(2, 4)
// 	return func(next echo.HandlerFunc) echo.HandlerFunc {
// 		return func(c echo.Context) error {
// 			if !limiter.Allow() {
// 				return echo.NewHTTPError(http.StatusTooManyRequests, "too many requests")
// 			} else {
// 				return next(c)
// 			}
// 		}
// 	}
// }

func (app *application) Authenticate() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Response().Header().Add("Vary", "Authorization")
			authHeader := c.Request().Header.Get("Authorization")
			if authHeader == "" {
				c.Set("user", data.AnonymousUser)
				return next(c)
			}
			headerParts := strings.Split(authHeader, " ")
			if len(headerParts) != 2 || headerParts[0] != "Bearer" {
				c.Response().Header().Set("WWW-Authenticate", "Bearer")
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid authentication token")
			}
			token := headerParts[1]
			v := validator.New()

			if data.ValidateTokenPlainText(v, token); !v.Valid() {
				c.Response().Header().Set("WWW-Authenticate", "Bearer")
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid authentication token")
			}

			user, err := app.models.Users.GetByToken(data.ScopeAuth, token)
			if err != nil {
				switch {
				case errors.Is(err, data.ErrNoRecordFound):
					c.Response().Header().Set("WWW-Authenticate", "Bearer")
					return echo.NewHTTPError(http.StatusUnauthorized, "invalid authentication token")
				default:
					return err
				}
			}

			c.Set("user", user)
			return next(c)

		}
	}
}

func (app *application) RequireActivatedUser(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		user := c.Get("user").(*data.User)
		if user.IsAnonymous() {
			return echo.NewHTTPError(http.StatusUnauthorized, "you must be autenticated to access this resource")
		}
		if !user.Activated {
			return echo.NewHTTPError(http.StatusForbidden, "your user account must be activated to access this resource")
		}
		return next(c)
	}
}

func (app *application) RequirePermission(permission string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		fn := func(c echo.Context) error {
			user := c.Get("user").(*data.User)
			permissions, err := app.models.Permissions.GetAllForUser(user.ID)
			if err != nil {
				return err
			}

			if !permissions.Include(permission) {
				return echo.NewHTTPError(http.StatusForbidden, "you user account doesn't have the necessary permissions to access this resource")
			}
			return next(c)
		}
		return app.RequireActivatedUser(fn)
	}
}
