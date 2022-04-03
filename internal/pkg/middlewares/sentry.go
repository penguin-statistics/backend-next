package middlewares

import (
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/gofiber/contrib/fibersentry"
	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp/fasthttpadaptor"

	"github.com/penguin-statistics/backend-next/internal/constant"
)

func EnrichSentry() func(ctx *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		if hub := fibersentry.GetHubFromContext(c); hub != nil {
			hub.Scope().SetTag("request_id", c.Locals(constant.ContextKeyRequestID).(string))
		}

		var r http.Request
		if err := fasthttpadaptor.ConvertRequest(c.Context(), &r, true); err != nil {
			return err
		}

		spanIgnored := c.Get(constant.SlimHeaderKey) != ""

		if spanIgnored {
			return c.Next()
		}

		span := sentry.StartSpan(c.Context(), "backend", sentry.ContinueFromRequest(&r), sentry.TransactionName(c.Method()+" "+c.Path()))
		span.SetTag("url", c.OriginalURL())
		defer span.Finish()

		return c.Next()
	}
}
