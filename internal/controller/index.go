package controller

import "github.com/gofiber/fiber/v2"

func RegisterIndexController(app *fiber.App) {
	app.Get("/api", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"@link":   "https://github.com/penguin-statistics/backend-next",
			"message": "Welcome to Penguin Stats API v3",
		})
	})
}
