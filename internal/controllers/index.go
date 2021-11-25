package controllers

import "github.com/gofiber/fiber/v2"

type IndexController struct {
}

func RegisterIndexController(app *fiber.App) {
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("Hello from Penguin v4!")
	})
}
