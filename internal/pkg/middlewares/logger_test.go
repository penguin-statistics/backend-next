package middlewares

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// newLoggedApp returns an app with the request logger and, inside it, the panic
// recoverer installed, writing to buf, and a pointer to the number of times the
// app's error handler ran. Unlike the service app there is no otelfiber, so a
// route's error reaches the logger as an error from an inner middleware would.
func newLoggedApp(t *testing.T, buf *bytes.Buffer) (*fiber.App, *int) {
	t.Helper()

	// injectLogger copies the global logger when the middleware is created
	prev := log.Logger
	log.Logger = zerolog.New(buf)
	t.Cleanup(func() { log.Logger = prev })

	errorHandlerCalls := 0
	app := fiber.New(fiber.Config{
		ErrorHandler: func(ctx *fiber.Ctx, err error) error {
			errorHandlerCalls++
			return fiber.DefaultErrorHandler(ctx, err)
		},
	})
	Logger(app)
	app.Use(recover.New())
	return app, &errorHandlerCalls
}

// requestLog returns the single "http.request" log line in buf.
func requestLog(t *testing.T, buf *bytes.Buffer) map[string]any {
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

// The response size histogram is observed after the error is handled too, so
// that it is labelled with the status class the client receives, not with the
// 2xx from before the error handler ran.
func TestRequestLoggerObservesResponseSizeWithStatusSent(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		handler     fiber.Handler
		statusClass string
		body        string
	}{
		{"runtime error", "/size-boom", func(ctx *fiber.Ctx) error { return errors.New("boom") }, "5xx", "boom"},
		{"fiber error", "/size-bad", func(ctx *fiber.Ctx) error { return fiber.ErrBadRequest }, "4xx", "Bad Request"},
		{"panic", "/size-panic", func(ctx *fiber.Ctx) error { panic("boom") }, "5xx", "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			app, _ := newLoggedApp(t, &buf)
			app.Get(tt.path, tt.handler)

			okCountBefore, _ := responseSize(t, tt.path, "GET", "2xx")
			countBefore, sumBefore := responseSize(t, tt.path, "GET", tt.statusClass)
			if _, err := app.Test(httptest.NewRequest("GET", tt.path, nil)); err != nil {
				t.Fatal(err)
			}
			okCountAfter, _ := responseSize(t, tt.path, "GET", "2xx")
			countAfter, sumAfter := responseSize(t, tt.path, "GET", tt.statusClass)

			if got := countAfter - countBefore; got != 1 {
				t.Errorf("%s response count increased by %d, want 1", tt.statusClass, got)
			}
			if got := sumAfter - sumBefore; got != float64(len(tt.body)) {
				t.Errorf("%s response size sum increased by %v, want %d", tt.statusClass, got, len(tt.body))
			}
			if got := okCountAfter - okCountBefore; got != 0 {
				t.Errorf("2xx response count increased by %d, want 0", got)
			}
			if got := requestLog(t, &buf)["http.route"]; got != tt.path {
				t.Errorf("logged http.route = %v, want %s", got, tt.path)
			}
		})
	}
}

func TestRequestLoggerLogsStatusAfterErrorHandler(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		handler fiber.Handler
		status  int
		// whether the chain returns an error for the error handler
		returnsErr bool
	}{
		{"ok", "/ok", func(ctx *fiber.Ctx) error { return ctx.SendString("ok") }, 200, false},
		{"status set by handler", "/teapot", func(ctx *fiber.Ctx) error { return ctx.SendStatus(418) }, 418, false},
		{"runtime error", "/boom", func(ctx *fiber.Ctx) error { return errors.New("boom") }, 500, true},
		{"fiber error", "/bad", func(ctx *fiber.Ctx) error { return fiber.ErrBadRequest }, 400, true},
		{"unmatched route", "/does-not-exist", nil, 404, true},
		{"panic", "/panic", func(ctx *fiber.Ctx) error { panic("boom") }, 500, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			app, errorHandlerCalls := newLoggedApp(t, &buf)
			if tt.handler != nil {
				app.Get(tt.path, tt.handler)
			}

			resp, err := app.Test(httptest.NewRequest("GET", tt.path, nil))
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tt.status {
				t.Errorf("response status = %d, want %d", resp.StatusCode, tt.status)
			}
			if got := requestLog(t, &buf)["http.status_code"]; got != float64(tt.status) {
				t.Errorf("logged http.status_code = %v, want %d", got, tt.status)
			}
			// the error handler must run exactly once, not again after the chain
			wantCalls := 0
			if tt.returnsErr {
				wantCalls = 1
			}
			if *errorHandlerCalls != wantCalls {
				t.Errorf("error handler ran %d times, want %d", *errorHandlerCalls, wantCalls)
			}
		})
	}
}
