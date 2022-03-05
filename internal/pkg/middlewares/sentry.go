package middlewares

import (
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/gofiber/contrib/fibersentry"
	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp/fasthttpadaptor"

	"github.com/penguin-statistics/backend-next/internal/constants"
)

func EnrichSentry() func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		if hub := fibersentry.GetHubFromContext(c); hub != nil {
			hub.Scope().SetTag("request_id", c.Locals(constants.ContextKeyRequestID).(string))
		}

		var r http.Request
		if err := fasthttpadaptor.ConvertRequest(c.Context(), &r, true); err != nil {
			return err
		}
		rootSpan := sentry.StartSpan(c.Context(), "backend", sentry.ContinueFromRequest(&r))
		defer rootSpan.Finish()

		return c.Next()
	}
}
