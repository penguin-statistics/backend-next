package middlewares

import (
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/util/rekuest"
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
