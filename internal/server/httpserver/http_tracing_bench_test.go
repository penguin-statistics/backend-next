package httpserver

import (
	"io"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"

	"exusiai.dev/backend-next/internal/app/appconfig"
)

// BenchmarkServiceAppTracing serves GET /items through the whole middleware chain of the service app at the
// production sample rate (0.1), with the spans recorded but not exported and the request log encoded as in production
// but discarded:
//   - plain: a request without trace context, as almost every request is (clients other than the RUM SDK)
//   - traceparent: a request of the RUM SDK, with a sampled traceparent and its tracestate
func BenchmarkServiceAppTracing(b *testing.B) {
	defer func(l zerolog.Logger) { log.Logger = l }(log.Logger)
	log.Logger = zerolog.New(io.Discard).With().Timestamp().Logger()

	conf := &appconfig.Config{}
	conf.CORSCredentialedOrigins = []string{"https://penguin-stats.io"}
	conf.TracingSampleRate = 0.1
	app := CreateServiceApp(conf)
	app.Get("/items", func(c *fiber.Ctx) error {
		return c.SendString("[]")
	})
	handler := app.Handler()

	for _, bc := range []struct {
		name    string
		headers map[string]string
	}{
		{"plain", nil},
		{"traceparent", map[string]string{
			"traceparent": "00-00000000000000004bf92f3577b34da6-00f067aa0ba902b7-01",
			"tracestate":  "dd=s:1;o:rum",
		}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			var req fasthttp.Request
			req.Header.SetMethod(fiber.MethodGet)
			req.SetRequestURI("/items")
			req.Header.Set(fiber.HeaderUserAgent, "Mozilla/5.0")
			req.Header.Set(fiber.HeaderAcceptEncoding, "gzip, br")
			for k, v := range bc.headers {
				req.Header.Set(k, v)
			}
			var ctx fasthttp.RequestCtx
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// a new request on the connection, as fasthttp serves it: Init does not drop the user values, which
				// hold fiber's user context
				ctx.ResetUserValues()
				ctx.Init(&req, nil, nil)
				handler(&ctx)
				if ctx.Response.StatusCode() != fiber.StatusOK {
					b.Fatalf("status %d", ctx.Response.StatusCode())
				}
			}
		})
	}
}
