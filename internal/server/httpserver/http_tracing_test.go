package httpserver

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"exusiai.dev/backend-next/internal/app/appconfig"
)

// newTracedServiceApp builds the real middleware chain at sampleRate, with GET /items, and records the spans of the
// tracer provider it installs.
func newTracedServiceApp(t *testing.T, sampleRate float64) (*fiber.App, *tracetest.SpanRecorder) {
	t.Helper()
	conf := &appconfig.Config{}
	conf.CORSCredentialedOrigins = defaultCredentialedOrigins(t)
	conf.TracingSampleRate = sampleRate
	app := CreateServiceApp(conf)
	app.Get("/items", func(c *fiber.Ctx) error {
		return c.SendString("[]")
	})

	provider, ok := otel.GetTracerProvider().(*tracesdk.TracerProvider)
	if !ok {
		t.Fatalf("global tracer provider is %T, want the SDK's", otel.GetTracerProvider())
	}
	recorder := tracetest.NewSpanRecorder()
	provider.RegisterSpanProcessor(recorder)
	return app, recorder
}

func serverSpan(t *testing.T, recorder *tracetest.SpanRecorder) tracesdk.ReadOnlySpan {
	t.Helper()
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d recorded spans, want the server span", len(spans))
	}
	return spans[0]
}

// A request carrying a W3C traceparent, as the Datadog RUM SDK sends with propagatorTypes ["tracecontext"], continues
// the browser's trace: the server span has its trace id and the browser's span as parent, so that the RUM resource
// finds it. The sampled flag decides, whatever the backend's own rate.
func TestServiceAppContinuesTheTraceOfTraceparent(t *testing.T) {
	const (
		traceID  = "00000000000000004bf92f3577b34da6"
		parentID = "00f067aa0ba902b7"
	)

	t.Run("sampled", func(t *testing.T) {
		// at rate 0 a request without traceparent is never sampled: the browser's flag alone samples this one
		app, recorder := newTracedServiceApp(t, 0)
		req := httptest.NewRequest(http.MethodGet, "/items", nil)
		req.Header.Set("traceparent", "00-"+traceID+"-"+parentID+"-01")
		req.Header.Set("tracestate", "dd=s:1;o:rum")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		span := serverSpan(t, recorder)
		if got := span.SpanContext().TraceID().String(); got != traceID {
			t.Errorf("trace id = %s, want the browser's %s", got, traceID)
		}
		if got := span.Parent().SpanID().String(); got != parentID || !span.Parent().IsRemote() {
			t.Errorf("parent = %s (remote %v), want the browser's span %s", got, span.Parent().IsRemote(), parentID)
		}
		if got := span.SpanContext().TraceState().Get("dd"); got != "s:1;o:rum" {
			t.Errorf("tracestate dd = %q, want it carried on", got)
		}
	})

	t.Run("not sampled", func(t *testing.T) {
		// at rate 1 every request without traceparent is sampled: the browser's flag alone leaves this one out
		app, recorder := newTracedServiceApp(t, 1)
		req := httptest.NewRequest(http.MethodGet, "/items", nil)
		req.Header.Set("traceparent", "00-"+traceID+"-"+parentID+"-00")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if n := len(recorder.Ended()); n != 0 {
			t.Errorf("recorded %d spans, want none: the browser did not sample the trace", n)
		}
	})

	t.Run("no traceparent", func(t *testing.T) {
		app, recorder := newTracedServiceApp(t, 1)
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/items", nil))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if span := serverSpan(t, recorder); span.Parent().IsValid() {
			t.Errorf("server span has parent %s, want a root span", span.Parent().SpanID())
		}
	})

	t.Run("malformed traceparent", func(t *testing.T) {
		app, recorder := newTracedServiceApp(t, 1)
		req := httptest.NewRequest(http.MethodGet, "/items", nil)
		req.Header.Set("traceparent", "00-not-a-trace-01")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != fiber.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
		if span := serverSpan(t, recorder); span.Parent().IsValid() {
			t.Errorf("server span has parent %s, want a root span", span.Parent().SpanID())
		}
	})
}

// The RUM SDK adds traceparent and tracestate to a cross-origin request too (a mirror or a preview deployment calling
// the API), which makes the browser send a preflight first: both tiers must allow them, without loosening the rest.
func TestServiceAppPreflightAllowsTraceContextHeaders(t *testing.T) {
	app := newCORSServiceApp(t)

	for _, tt := range []struct {
		origin       string
		credentialed bool
	}{
		{"https://penguin-stats.io", true},
		{"https://1b5f51bd.frontend-v2-8db.pages.dev", true},
		{"https://evil.example", false},
	} {
		t.Run(tt.origin, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, "/stats", nil)
			req.Header.Set(fiber.HeaderOrigin, tt.origin)
			req.Header.Set(fiber.HeaderAccessControlRequestMethod, fiber.MethodGet)
			req.Header.Set(fiber.HeaderAccessControlRequestHeaders, "traceparent,tracestate")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != fiber.StatusNoContent {
				t.Errorf("status = %d, want 204", resp.StatusCode)
			}
			allowed := strings.Split(resp.Header.Get(fiber.HeaderAccessControlAllowHeaders), ",")
			for _, h := range []string{"traceparent", "tracestate", "Authorization"} {
				if !containsFold(allowed, h) {
					t.Errorf("Access-Control-Allow-Headers = %q, want it to hold %s", allowed, h)
				}
			}
			if containsFold(allowed, "*") {
				t.Errorf("Access-Control-Allow-Headers = %q, want no wildcard", allowed)
			}
			assertCORS(t, resp, tt.origin, tt.credentialed)
		})
	}
}

func containsFold(values []string, s string) bool {
	for _, v := range values {
		if strings.EqualFold(strings.TrimSpace(v), s) {
			return true
		}
	}
	return false
}

// The request log of a sampled request carries the ids of its server span as Datadog correlates logs and traces
// (dd.trace_id: the lower 64 bits of the trace id, dd.span_id, in decimal), so that the log links to the trace. A request
// not sampled logs none: its trace is not exported.
func TestRequestLogCarriesTraceIDs(t *testing.T) {
	var logs bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&logs)
	t.Cleanup(func() { log.Logger = prev })

	requestLog := func(t *testing.T) map[string]any {
		t.Helper()
		var m map[string]any
		for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
			var entry map[string]any
			if err := json.Unmarshal(line, &entry); err != nil {
				t.Fatal(err)
			}
			if entry["evt.name"] == "http.request" {
				m = entry
			}
		}
		if m == nil {
			t.Fatalf("no request log in %s", logs.String())
		}
		return m
	}

	t.Run("sampled", func(t *testing.T) {
		logs.Reset()
		app, recorder := newTracedServiceApp(t, 0)
		req := httptest.NewRequest(http.MethodGet, "/items", nil)
		req.Header.Set("traceparent", "00-00000000000000008448eb211c80319c-b7ad6b7169203331-01")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		span := serverSpan(t, recorder)
		m := requestLog(t)
		if got, want := m["dd.trace_id"], "9532127138774266268"; got != want {
			t.Errorf("dd.trace_id = %v, want %s, the trace id 8448eb211c80319c in decimal", got, want)
		}
		sid := span.SpanContext().SpanID()
		if got, want := m["dd.span_id"], strconv.FormatUint(binary.BigEndian.Uint64(sid[:]), 10); got != want {
			t.Errorf("dd.span_id = %v, want the server span's %s", got, want)
		}
	})

	t.Run("not sampled", func(t *testing.T) {
		logs.Reset()
		app, _ := newTracedServiceApp(t, 0)
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/items", nil))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		m := requestLog(t)
		if _, ok := m["dd.trace_id"]; ok {
			t.Errorf("request log %v, want no trace id of a request not sampled", m)
		}
	})
}
