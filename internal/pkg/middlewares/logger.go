package middlewares

import (
	"strconv"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/pkg/flog"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/pkg/pgid"
)

// maxLoggedOriginLength bounds the Origin header in the request log, as the client sets it.
const maxLoggedOriginLength = 256

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
		level := requestLogLevel(status)
		// fasthttp does not tell the handler that the client went away, and writes
		// the response only after this returns, so check the connection here: the
		// status logged was never delivered to a peer that has already closed it.
		// Behind ingress-nginx that is a 499 (client gave up) or a 504 (ingress
		// upstream timeout); the ingress log has which.
		closed := peerClosed(ctx.Context().Conn())
		if closed && level < zerolog.WarnLevel {
			level = zerolog.WarnLevel
		}

		evt := flog.FromFiberCtx(ctx).WithLevel(level).
			Str("evt.name", "http.request")
		if closed {
			evt = evt.Bool("http.peer_closed", true)
		}
		// usr.id is the PenguinID; the routes that resolve the account into ctx.Locals (the
		// report routes) also log its internal id, which joins drop_reports.account_id
		if accountId, ok := ctx.Locals(constant.LocalsAccountIDKey).(int); ok {
			evt = evt.Str("usr.account_id", strconv.Itoa(accountId))
		}
		// browser requests only (MAA and other clients send no Origin): which origins call the API, and which
		// tier CORS gave them. A page outside the allowlist that sends credentials fails in the browser while
		// this still logs 200, so these fields are the only trace of it.
		if origin := ctx.Get(fiber.HeaderOrigin); origin != "" {
			if len(origin) > maxLoggedOriginLength {
				origin = origin[:maxLoggedOriginLength]
			}
			tier, _ := ctx.Locals(localsKeyCORSTier).(string)
			evt = evt.Str("http.origin", origin).Str("http.cors_tier", tier)
		}
		if pgid.IgnoredCookie(ctx) {
			// see pgid.IgnoreCookie: a cross-site request that carried the PenguinID cookie
			evt = evt.Bool("usr.cookie_ignored", true)
		}
		evt.
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

// requestLogLevel maps a response status to the request log level, so that
// server errors show up as errors: 5xx error, 4xx warn, anything else info.
func requestLogLevel(status int) zerolog.Level {
	switch {
	case status >= 500:
		return zerolog.ErrorLevel
	case status >= 400:
		return zerolog.WarnLevel
	default:
		return zerolog.InfoLevel
	}
}
