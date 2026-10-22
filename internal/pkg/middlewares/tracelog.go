package middlewares

import (
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/trace"

	"exusiai.dev/backend-next/internal/pkg/flog"
	"exusiai.dev/backend-next/internal/pkg/observability"
)

// LogTraceIDs adds the trace and span ids of the request's server span to the request's logger (Logger), if the span
// is sampled, so that the request log and the lines logged with flog link to the request's trace in Datadog, and the
// trace to them. It must come after the tracing middleware (otelfiber), which starts the span.
func LogTraceIDs() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if sc := trace.SpanContextFromContext(c.UserContext()); sc.IsSampled() {
			flog.UpdateContext(c, func(lc zerolog.Context) zerolog.Context {
				return observability.WithTraceIDs(lc, sc)
			})
		}
		return c.Next()
	}
}
