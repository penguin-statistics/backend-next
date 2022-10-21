package util

import (
	"github.com/gofiber/fiber/v2"

	"exusiai.dev/gommon/constant"
)

func IdempotencyKeyFromLocals(ctx *fiber.Ctx) string {
	l, ok := ctx.Locals(constant.IdempotencyKeyLocalsKey).(string)
	if !ok {
		return ""
	}

	return l
}
