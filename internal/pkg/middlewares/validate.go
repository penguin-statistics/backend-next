package middlewares

import (
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/utils/rekuest"
)

func ValidateServer(c *fiber.Ctx) error {
	if err := rekuest.ValidServer(c, c.Params("server")); err != nil {
		return err
	}
	return c.Next()
}
