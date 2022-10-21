package meta

import "github.com/gofiber/fiber/v2"

func RegisterIndex(app *fiber.App) {
	app.Get("/api", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"@link":   "https://exusiai.dev/backend-next",
			"message": "Welcome to Penguin Stats API v3",
		})
	})
}
