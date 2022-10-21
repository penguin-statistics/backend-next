package middlewares

import (
	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/util/rekuest"
	"exusiai.dev/gommon/constant"
)

func ValidateServerAsParam(c *fiber.Ctx) error {
	if err := rekuest.ValidServer(c, c.Params("server")); err != nil {
		return err
	}
	return c.Next()
}

func ValidateServerAsQuery(c *fiber.Ctx) error {
	server := c.Query("server", constant.DefaultServer)
	if err := rekuest.ValidServer(c, server); err != nil {
		return err
	}
	return c.Next()
}

func ValidateCategoryAsParam(c *fiber.Ctx) error {
	if err := rekuest.ValidCategory(c, c.Params("category", "all")); err != nil {
		return err
	}
	return c.Next()
}
