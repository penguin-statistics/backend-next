package middlewares

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/pkg/flog"
)

func Logger(app *fiber.App) {
	Chained(
		app,
		injectLogger(),
		flog.RequestIDHandler("request_id", "X-Penguin-Request-ID"),
		flog.RemoteAddrHandler("ip"),
		flog.MethodHandler("method"),
		flog.URLHandler("url"),
		flog.UserAgentHandler("user_agent"),
		requestLogger(),
	)
}

func injectLogger() func(ctx *fiber.Ctx) error {
	return flog.NewHandlerMiddleware(log.With().Logger())
}

func requestLogger() func(ctx *fiber.Ctx) error {
	return flog.AccessHandler(func(ctx *fiber.Ctx, duration time.Duration) {
		flog.FromFiberCtx(ctx).Info().
			Str("component", "httpreq").
			Int("status", ctx.Response().StatusCode()).
			Int("size", len(ctx.Response().Body())).
			Dur("duration", duration).
			Msg("received request")
	})
}
