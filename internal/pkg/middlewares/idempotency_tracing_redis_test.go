package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/extra/redisotel/v9"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// The Redis scripts of the lock of an idempotency key are traced under the request's span when it is sampled, as
// infra.Redis instruments the client, and are not traced at all when it is not.
func TestIdempotencyLockIsTracedInTheRequest(t *testing.T) {
	client := newTestIdempotencyRedis(t)
	recorder := tracetest.NewSpanRecorder()
	provider := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(recorder))
	if err := redisotel.InstrumentTracing(client, redisotel.WithTracerProvider(provider)); err != nil {
		t.Fatal(err)
	}
	prefix := testIdempotencyPrefix(t, client)

	for _, sampled := range []bool{true, false} {
		app := fiber.New()
		var request trace.Span
		app.Use(func(c *fiber.Ctx) error {
			sampler := tracesdk.NeverSample()
			if sampled {
				sampler = tracesdk.AlwaysSample()
			}
			ctx, span := tracesdk.NewTracerProvider(tracesdk.WithSampler(sampler), tracesdk.WithSpanProcessor(recorder)).
				Tracer("test").Start(c.UserContext(), "request")
			request = span
			c.SetUserContext(ctx)
			defer span.End()
			return c.Next()
		})
		app.Post("/", Idempotency(testIdempotencyConfig(client, prefix)), func(c *fiber.Ctx) error {
			return c.SendString("ok")
		})
		before := len(recorder.Ended())
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set(constant.IdempotencyKeyHeader, "key"+map[bool]string{true: "sampled", false: "notsampled"}[sampled])
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		var scripts int
		for _, span := range recorder.Ended()[before:] {
			// a new connection's redis.dial is a child of the command that dialed it
			if span.Name() == "request" || span.Name() == "redis.dial" {
				continue
			}
			scripts++
			if span.Parent().SpanID() != request.SpanContext().SpanID() {
				t.Errorf("%s span of parent %s, want the request's span", span.Name(), span.Parent().SpanID())
			}
		}
		// acquire and save, each an EVALSHA (and an EVAL the first time a script runs on the server)
		if sampled && scripts < 2 {
			t.Errorf("sampled request: %d Redis spans, want those of acquiring and saving the lock", scripts)
		}
		if !sampled && scripts != 0 {
			t.Errorf("request not sampled: %d Redis spans, want none", scripts)
		}
	}
}
