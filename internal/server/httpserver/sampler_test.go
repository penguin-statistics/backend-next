package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"go.opentelemetry.io/otel/trace"

	"exusiai.dev/backend-next/internal/app/appconfig"
)

func TestAlwaysSampleNamePrefix(t *testing.T) {
	recorder := func(rate float64) (*tracesdk.TracerProvider, *[]string) {
		names := &[]string{}
		provider := tracesdk.NewTracerProvider(
			tracesdk.WithSampler(tracesdk.ParentBased(alwaysSampleNamePrefix{
				prefix:   "calcwkr.",
				fallback: tracesdk.TraceIDRatioBased(rate),
			})),
			tracesdk.WithSpanProcessor(spanNameRecorder{names}),
		)
		return provider, names
	}

	provider, names := recorder(0)
	tracer := provider.Tracer("test")
	ctx, batch := tracer.Start(context.Background(), "calcwkr.batch")
	_, query := tracer.Start(ctx, "SELECT")
	query.End()
	batch.End()
	for i := 0; i < 100; i++ {
		_, request := tracer.Start(context.Background(), "GET /PenguinStats/api/v2/result/matrix")
		request.End()
	}
	if len(*names) != 2 || (*names)[0] != "SELECT" || (*names)[1] != "calcwkr.batch" {
		t.Errorf("at rate 0, got sampled spans %v, want only the worker batch and its query", *names)
	}

	provider, names = recorder(1)
	_, request := provider.Tracer("test").Start(context.Background(), "GET /PenguinStats/api/v2/result/matrix")
	request.End()
	if len(*names) != 1 {
		t.Errorf("at rate 1, got sampled spans %v, want the request", *names)
	}
}

// spanNameRecorder records the names of sampled spans as they end.
type spanNameRecorder struct{ names *[]string }

func (r spanNameRecorder) OnStart(context.Context, tracesdk.ReadWriteSpan) {}
func (r spanNameRecorder) OnEnd(s tracesdk.ReadOnlySpan) {
	if s.SpanContext().TraceFlags()&trace.FlagsSampled != 0 {
		*r.names = append(*r.names, s.Name())
	}
}
func (r spanNameRecorder) Shutdown(context.Context) error   { return nil }
func (r spanNameRecorder) ForceFlush(context.Context) error { return nil }

// fakeClock is a clock that moves only when told to.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

// A request to a path other than the report submission and the probes is sampled whatever the rate, up to the limit
// per second; the report submission, the probes and any span that is not a request's are left to the rate.
func TestLowVolumeRouteSampler(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1760000000, 0)}
	sampler := lowVolumeRouteSampler{fallback: tracesdk.TraceIDRatioBased(0), limiter: newTokenBucket(2, clock.now)}
	sampled := func(kind trace.SpanKind, method, path string) bool {
		var traceID trace.TraceID
		traceID[15] = 1
		return sampler.ShouldSample(tracesdk.SamplingParameters{
			TraceID:    traceID,
			Name:       path,
			Kind:       kind,
			Attributes: []attribute.KeyValue{semconv.HTTPSchemeHTTP, semconv.HTTPMethodKey.String(method)},
		}).Decision == tracesdk.RecordAndSample
	}

	for _, tt := range []struct {
		kind         trace.SpanKind
		method, path string
	}{
		{trace.SpanKindServer, "POST", "/PenguinStats/api/v2/report"},
		{trace.SpanKindServer, "POST", "/penguinstats/api/v2/report/"},
		{trace.SpanKindServer, "GET", "/api/_/health"},
		{trace.SpanKindServer, "GET", "/metrics"},
		{trace.SpanKindConsumer, "", "reportwkr.ConsumeTask"},
		{trace.SpanKindClient, "", ""},
	} {
		if sampled(tt.kind, tt.method, tt.path) {
			t.Errorf("%v %s %s sampled, want it left to the rate (0)", tt.kind, tt.method, tt.path)
		}
	}

	// the limit: 2 per second
	if !sampled(trace.SpanKindServer, "GET", "/PenguinStats/api/v2/stages") || !sampled(trace.SpanKindServer, "POST", "/PenguinStats/api/v2/report/recognition") {
		t.Error("requests to low-volume routes not sampled")
	}
	if sampled(trace.SpanKindServer, "GET", "/zh/result/stage/main/main_01-07") {
		t.Error("third request within a second sampled, over the limit of 2")
	}
	clock.t = clock.t.Add(500 * time.Millisecond)
	if !sampled(trace.SpanKindServer, "GET", "/zh/result/stage/main/main_01-07") {
		t.Error("request half a second later not sampled: a token is back")
	}
	if sampled(trace.SpanKindServer, "GET", "/zh/result/stage/main/main_01-07") {
		t.Error("second request half a second later sampled, over the limit")
	}
	clock.t = clock.t.Add(time.Hour)
	for i := 0; i < 2; i++ {
		if !sampled(trace.SpanKindServer, "GET", "/sitemap.xml") {
			t.Errorf("request %d after a quiet hour not sampled", i)
		}
	}
	if sampled(trace.SpanKindServer, "GET", "/sitemap.xml") {
		t.Error("the burst after a quiet hour exceeds the limit of a second")
	}
}

// Through the middleware chain at rate 0: a read of the API is traced, the report submission is not.
func TestServiceAppSamplesTheLowVolumeRoutes(t *testing.T) {
	conf := &appconfig.Config{}
	conf.CORSCredentialedOrigins = defaultCredentialedOrigins(t)
	conf.TracingSampleRate = 0
	conf.TracingLowVolumeTracesPerSecond = 10
	app := CreateServiceApp(conf)
	app.Get("/items", func(c *fiber.Ctx) error { return c.SendString("[]") })
	app.Post("/PenguinStats/api/v2/report", func(c *fiber.Ctx) error { return c.SendString("{}") })
	provider := otel.GetTracerProvider().(*tracesdk.TracerProvider)
	recorder := tracetest.NewSpanRecorder()
	provider.RegisterSpanProcessor(recorder)

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/PenguinStats/api/v2/report", strings.NewReader("{}")),
		httptest.NewRequest(http.MethodGet, "/items", nil),
	} {
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "/items" {
		names := make([]string, len(spans))
		for i, s := range spans {
			names[i] = s.Name()
		}
		t.Errorf("sampled %v, want the read of /items only", names)
	}
}
