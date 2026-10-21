package middlewares

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/pkg/observability"
)

// newRouteLoggedApp returns an app with the request logger writing to buf and
// these routes:
//   - GET and POST /stages/:stageId answer "ok", or 404 "not found" for stageId
//     "missing"
//   - GET /cached/items/:itemId sits behind a group middleware that answers
//     "hit" without calling Next, like the fiber cache on a hit or a limiter
//     rejection
func newRouteLoggedApp(t *testing.T, buf *bytes.Buffer) *fiber.App {
	t.Helper()

	// injectLogger copies the global logger when the middleware is created
	prev := log.Logger
	log.Logger = zerolog.New(buf)
	t.Cleanup(func() { log.Logger = prev })

	app := fiber.New()
	Logger(app)
	stage := func(ctx *fiber.Ctx) error {
		if ctx.Params("stageId") == "missing" {
			return ctx.Status(fiber.StatusNotFound).SendString("not found")
		}
		return ctx.SendString("ok")
	}
	app.Get("/stages/:stageId", stage)
	app.Post("/stages/:stageId", stage)

	cached := app.Group("/cached")
	cached.Use(func(ctx *fiber.Ctx) error {
		return ctx.SendString("hit")
	})
	cached.Get("/items/:itemId", func(ctx *fiber.Ctx) error {
		return ctx.SendString("miss")
	})
	return app
}

// requestLogLine returns the single "http.request" log line in buf.
func requestLogLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	var found []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		if m["evt.name"] == "http.request" {
			found = append(found, m)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want 1 request log, got %d: %s", len(found), buf.String())
	}
	return found[0]
}

func TestRequestLoggerLogsRouteTemplate(t *testing.T) {
	tests := []struct {
		name  string
		url   string
		route string
		body  string
	}{
		{"handler", "/stages/main_01-07?server=CN", "/stages/:stageId", "ok"},
		// a group middleware that answers without calling Next leaves its own
		// prefix as the route, not the template of the route it was sent to
		{"group middleware answered", "/cached/items/30012", "/cached", "hit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			app := newRouteLoggedApp(t, &buf)

			resp, err := app.Test(httptest.NewRequest("GET", tt.url, nil))
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != fiber.StatusOK {
				t.Fatalf("response status = %d, want 200", resp.StatusCode)
			}

			line := requestLogLine(t, &buf)
			if got := line["http.route"]; got != tt.route {
				t.Errorf("logged http.route = %v, want %s", got, tt.route)
			}
			// the existing fields are unchanged
			if got := line["http.url"]; got != tt.url {
				t.Errorf("logged http.url = %v, want the raw URL", got)
			}
			if got := line["http.status_code"]; got != float64(fiber.StatusOK) {
				t.Errorf("logged http.status_code = %v, want 200", got)
			}
			if got := line["network.bytes_written"]; got != float64(len(tt.body)) {
				t.Errorf("logged network.bytes_written = %v, want %d", got, len(tt.body))
			}
			// usr.id is left out: whether it is logged does not depend on this change
			for _, k := range []string{
				"level", "evt.name", "message", "duration", "http.method", "http.request_id",
				"http.useragent", "network.bytes_read", "network.client.ip",
			} {
				if _, ok := line[k]; !ok {
					t.Errorf("logged fields lack %s: %v", k, line)
				}
			}
		})
	}
}

// responseSize returns the sample count and sum of the response size histogram
// for the given labels.
func responseSize(t *testing.T, route, method, statusClass string) (uint64, float64) {
	t.Helper()

	var m dto.Metric
	h := observability.HTTPResponseSize.WithLabelValues(route, method, statusClass).(prometheus.Metric)
	if err := h.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram().GetSampleCount(), m.GetHistogram().GetSampleSum()
}

func TestRequestLoggerObservesResponseSizeByRoute(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		url         string
		route       string
		statusClass string
		body        string
	}{
		{"2xx", "GET", "/stages/main_01-07", "/stages/:stageId", "2xx", "ok"},
		{"4xx", "GET", "/stages/missing", "/stages/:stageId", "4xx", "not found"},
		{"POST", "POST", "/stages/main_01-07", "/stages/:stageId", "2xx", "ok"},
		{"group middleware answered", "GET", "/cached/items/30012", "/cached", "2xx", "hit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			app := newRouteLoggedApp(t, &buf)

			countBefore, sumBefore := responseSize(t, tt.route, tt.method, tt.statusClass)
			if _, err := app.Test(httptest.NewRequest(tt.method, tt.url, nil)); err != nil {
				t.Fatal(err)
			}
			countAfter, sumAfter := responseSize(t, tt.route, tt.method, tt.statusClass)

			if got := countAfter - countBefore; got != 1 {
				t.Errorf("response count increased by %d, want 1", got)
			}
			if got := sumAfter - sumBefore; got != float64(len(tt.body)) {
				t.Errorf("response size sum increased by %v, want %d", got, len(tt.body))
			}
		})
	}
}
