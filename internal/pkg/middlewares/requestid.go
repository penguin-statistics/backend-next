package middlewares

import (
	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/pkg/flog"
	"exusiai.dev/gommon/constant"
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
