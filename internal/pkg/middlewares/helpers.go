package middlewares

import (
	"github.com/gofiber/fiber/v2"
)

func Chained(app *fiber.App, middlewares ...fiber.Handler) {
	for _, middleware := range middlewares {
		app.Use(middleware)
	}
}
