package middlewares

import (
	"github.com/gofiber/contrib/fibersentry"
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/constants"
)

func EnrichSentry() func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		if hub := fibersentry.GetHubFromContext(c); hub != nil {
			hub.Scope().SetTag("request_id", c.Locals(constants.ContextKeyRequestID).(string))
		}
		return c.Next()
	}
}
