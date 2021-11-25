package utils

import "github.com/gofiber/fiber/v2"

func RespondBadRequest(ctx *fiber.Ctx, message string) error {
	return ctx.Status(fiber.StatusBadRequest).JSON(map[string]string{
		"error": message,
	})
}
