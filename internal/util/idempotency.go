package util

import (
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/constant"
)

func IdempotencyKeyFromLocals(ctx *fiber.Ctx) string {
	l, ok := ctx.Locals(constant.IdempotencyKeyLocalsKey).(string)
	if !ok {
		return ""
	}

	return l
}
