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
		flog.InfoFrom(ctx, "http.request").
			Bytes("http.useragent", ctx.Request().Header.UserAgent()).
			Int("http.status_code", ctx.Response().StatusCode()).
			Int("network.bytes_read", len(ctx.Request().Body())).
			Int("network.bytes_written", len(ctx.Response().Body())).
			Dur("duration", duration).
			Msg("received request")
	})
}
