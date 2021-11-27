package controllers

import "github.com/gofiber/fiber/v2"

type IndexController struct {
}

func RegisterIndexController(app *fiber.App) {
	app.Get("/", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"@link":   "https://github.com/penguin-statistics/backend-next",
			"message": "Welcome to Penguin Stats API v4",
		})
	})
}
