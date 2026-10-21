package middlewares

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/pkg/flog"
	"exusiai.dev/backend-next/internal/pkg/observability"
)

func Logger(app *fiber.App) {
	Chained(
		app,
		injectLogger(),
		flog.RequestIDHandler("http.request_id", "X-Penguin-Request-ID"),
		flog.PenguinIDHandler("usr.id"),
		flog.RemoteAddrHandler("network.client.ip"),
		flog.MethodHandler("http.method"),
		flog.URLHandler("http.url"),
		requestLogger(),
	)
}

func injectLogger() func(ctx *fiber.Ctx) error {
	return flog.NewHandlerMiddleware(log.With().Logger())
}

func requestLogger() func(ctx *fiber.Ctx) error {
	return flog.AccessHandler(func(ctx *fiber.Ctx, duration time.Duration) {
		// the last matched route, never the raw URL, so that it stays bounded and can
		// be grouped on. It is the handler's template (e.g.
		// /stages/:stageId) unless a group or app middleware answered without calling
		// Next: then it is that middleware's prefix, e.g. /PenguinStats/api/v2/result
		// for a cache hit or limiter rejection there, or / or a group prefix for a path
		// no route matches.
		route := ctx.Route().Path
		status := ctx.Response().StatusCode()
		bytesWritten := len(ctx.Response().Body())

		flog.InfoFrom(ctx, "http.request").
			Bytes("http.useragent", ctx.Request().Header.UserAgent()).
			Str("http.route", route).
			Int("http.status_code", status).
			Int("network.bytes_read", len(ctx.Request().Body())).
			Int("network.bytes_written", bytesWritten).
			Dur("duration", duration).
			Msg("received request")

		observability.HTTPResponseSize.
			WithLabelValues(route, ctx.Method(), observability.HTTPStatusClass(status)).
			Observe(float64(bytesWritten))
	})
}
