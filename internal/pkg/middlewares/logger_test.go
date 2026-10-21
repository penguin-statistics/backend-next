package middlewares

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const testReadTimeout = 200 * time.Millisecond

// newLoggedApp returns an app with the request logger and, inside it, the panic
// recoverer installed, writing to buf, and a pointer to the number of times the
// app's error handler ran. Unlike the service app there is no otelfiber, so a
// route's error reaches the logger as an error from an inner middleware would.
func newLoggedApp(t *testing.T, buf io.Writer) (*fiber.App, *int) {
	t.Helper()

	// injectLogger copies the global logger when the middleware is created
	prev := log.Logger
	log.Logger = zerolog.New(buf)
	t.Cleanup(func() { log.Logger = prev })

	errorHandlerCalls := 0
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		// as in production (20s there), so that a request handled for longer has
		// its read deadline in the past when the request logger runs
		ReadTimeout: testReadTimeout,
		// as in production, without the trusted proxy check
		ProxyHeader: "X-Original-Forwarded-For",
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
			entry := requestLog(t, &buf)
			if got := entry["http.status_code"]; got != float64(tt.status) {
				t.Errorf("logged http.status_code = %v, want %d", got, tt.status)
			}
			if got, want := entry["level"], requestLogLevel(tt.status).String(); got != want {
				t.Errorf("logged level = %v, want %s", got, want)
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

func TestRequestLogLevel(t *testing.T) {
	tests := []struct {
		status int
		want   zerolog.Level
	}{
		{100, zerolog.InfoLevel},
		{200, zerolog.InfoLevel},
		{204, zerolog.InfoLevel},
		{304, zerolog.InfoLevel},
		{399, zerolog.InfoLevel},
		{400, zerolog.WarnLevel},
		{404, zerolog.WarnLevel},
		{429, zerolog.WarnLevel},
		{499, zerolog.WarnLevel},
		{500, zerolog.ErrorLevel},
		{503, zerolog.ErrorLevel},
		{599, zerolog.ErrorLevel},
	}
	for _, tt := range tests {
		if got := requestLogLevel(tt.status); got != tt.want {
			t.Errorf("requestLogLevel(%d) = %s, want %s", tt.status, got, tt.want)
		}
	}
}

func TestRequestLoggerTruncatesClientIP(t *testing.T) {
	tests := []struct {
		forwardedFor string
		want         string
	}{
		{"203.0.113.77", "203.0.113.0"},
		{"203.0.113.77, 10.0.0.1", "203.0.113.0"},
		{"2001:db8:1234:5678::1", "2001:db8:1234::"},
	}
	for _, tt := range tests {
		var buf bytes.Buffer
		app, _ := newLoggedApp(t, &buf)
		app.Get("/", func(ctx *fiber.Ctx) error { return ctx.SendString("ok") })

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Original-Forwarded-For", tt.forwardedFor)
		if _, err := app.Test(req); err != nil {
			t.Fatal(err)
		}
		if got := requestLog(t, &buf)["network.client.ip"]; got != tt.want {
			t.Errorf("X-Original-Forwarded-For %q: logged network.client.ip = %v, want %s", tt.forwardedFor, got, tt.want)
		}
	}
}

func TestRequestLoggerLogsAccountIDNotPenguinID(t *testing.T) {
	const penguinId = "87654321"

	var buf bytes.Buffer
	app, _ := newLoggedApp(t, &buf)
	app.Get("/anonymous", func(ctx *fiber.Ctx) error { return ctx.SendString("ok") })
	app.Get("/account", func(ctx *fiber.Ctx) error {
		// as Report.MiddlewareGetOrCreateAccount does
		ctx.Locals(constant.LocalsAccountIDKey, 4242)
		return ctx.SendString("ok")
	})

	tests := []struct {
		path string
		want any
	}{
		{"/anonymous", nil},
		{"/account", "4242"},
	}
	for _, tt := range tests {
		buf.Reset()
		req := httptest.NewRequest("GET", tt.path, nil)
		req.Header.Set(fiber.HeaderAuthorization, constant.PenguinIDAuthorizationRealm+penguinId)
		req.AddCookie(&http.Cookie{Name: constant.PenguinIDCookieKey, Value: penguinId})
		if _, err := app.Test(req); err != nil {
			t.Fatal(err)
		}
		if got := requestLog(t, &buf)["usr.id"]; got != tt.want {
			t.Errorf("%s: logged usr.id = %v, want %v", tt.path, got, tt.want)
		}
		if strings.Contains(buf.String(), penguinId) {
			t.Errorf("%s: PenguinID logged: %s", tt.path, buf.String())
		}
	}
}
