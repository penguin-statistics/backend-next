package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
)

// TestServiceAppLogsStatusSent runs requests through the service app's real
// middleware chain and checks that the request log records the status the
// client received, and that the error handler handles an error exactly once.
func TestServiceAppLogsStatusSent(t *testing.T) {
	var buf bytes.Buffer
	// the logger middleware copies the global logger when the app is created
	prev := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = prev })

	app := CreateServiceApp(&appconfig.Config{ConfigSpec: appconfig.ConfigSpec{DevMode: true}})
	app.Get("/ok", func(ctx *fiber.Ctx) error { return ctx.SendString("ok") })
	app.Get("/boom", func(ctx *fiber.Ctx) error { return errors.New("boom") })
	app.Get("/bad", func(ctx *fiber.Ctx) error { return fiber.ErrBadRequest })
	app.Get("/invalid", func(ctx *fiber.Ctx) error { return pgerr.ErrInvalidReq })
	app.Get("/panic", func(ctx *fiber.Ctx) error { panic("boom") })

	tests := []struct {
		path   string
		status int
		// "Internal Server Error" logs written by ErrorHandler, once per runtime error
		internalErrorLogs int
	}{
		{"/ok", 200, 0},
		{"/boom", 500, 1},
		{"/bad", 400, 0},
		{"/invalid", 400, 0},
		{"/does-not-exist", 404, 0},
		// recover sits outside otelfiber, so the error it makes of a panic is
		// only handled after the logger unless the logger handles it itself
		{"/panic", 500, 1},
	}
	for _, tt := range tests {
		buf.Reset()
		resp, err := app.Test(httptest.NewRequest("GET", tt.path, nil), -1)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != tt.status {
			t.Errorf("%s: response status = %d, want %d", tt.path, resp.StatusCode, tt.status)
		}

		var requestLogs []map[string]any
		internalErrorLogs := 0
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				// the panic stack trace is not a single JSON line
				continue
			}
			if m["evt.name"] == "http.request" {
				requestLogs = append(requestLogs, m)
			}
			if m["message"] == "Internal Server Error" {
				internalErrorLogs++
			}
		}
		if len(requestLogs) != 1 {
			t.Fatalf("%s: want 1 request log, got %d: %s", tt.path, len(requestLogs), buf.String())
		}
		if got := requestLogs[0]["http.status_code"]; got != float64(tt.status) {
			t.Errorf("%s: logged http.status_code = %v, want %d", tt.path, got, tt.status)
		}
		if internalErrorLogs != tt.internalErrorLogs {
			t.Errorf("%s: %d Internal Server Error logs, want %d", tt.path, internalErrorLogs, tt.internalErrorLogs)
		}
	}
}
