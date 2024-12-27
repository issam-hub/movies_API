package main

import (
	"github.com/labstack/echo/v4"
)

func (app *application) routes(e *echo.Echo) {
	router := e.Group("/v1")

	router.GET("/movies", app.getMoviesHandler, app.RequirePermission("movies:read"))
	router.POST("/movies", app.createMovieHandler, app.RequirePermission("movies:write"))
	router.GET("/movies/:id", app.showMovieHandler, app.RequirePermission("movies:read"))
	router.PATCH("/movies/:id", app.updateMovieHandler, app.RequirePermission("movies:write"))
	router.DELETE("/movies/:id", app.deleteMovieHandler, app.RequirePermission("movies:write"))

	router.POST("/users", app.registerUserHandler)
	router.PUT("/users/activated", app.activateUserHandler)
	router.POST("/users/authentication", app.authenticationTokenHandler)
}
