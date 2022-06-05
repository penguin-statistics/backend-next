package middlewares

import (
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/pkg/flog"
)

func RequestID() fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, ok := flog.IDFromFiberCtx(c)
		if ok {
			c.Locals(constant.ContextKeyRequestID, id.String())
		}
		return c.Next()
	}
}
