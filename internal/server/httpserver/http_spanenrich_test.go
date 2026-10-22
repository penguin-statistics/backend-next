package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	"go.opentelemetry.io/otel/codes"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"

	"exusiai.dev/backend-next/internal/pkg/middlewares"
)

func spanAttributes(span tracesdk.ReadOnlySpan) map[string]string {
	got := map[string]string{}
	for _, kv := range span.Attributes() {
		got[string(kv.Key)] = kv.Value.Emit()
	}
	return got
}

// The server span of a sampled request names who made it: the client's address, the PenguinID it was made with and
// the account a report route resolved.
func TestServerSpanNamesTheUser(t *testing.T) {
	app, recorder := newTracedServiceApp(t, 1)
	app.Get("/account", func(c *fiber.Ctx) error {
		c.Locals(constant.LocalsAccountIDKey, 42)
		return c.SendString("ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/account", nil)
	req.Header.Set(fiber.HeaderAuthorization, constant.PenguinIDAuthorizationRealm+"12345678")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	got := spanAttributes(serverSpan(t, recorder))
	for k, want := range map[string]string{"usr.id": "12345678", "usr.account_id": "42", "http.client_ip": "0.0.0.0"} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	if _, ok := got["http.idempotency"]; ok {
		t.Error("http.idempotency set on a request without an idempotency key")
	}
}

// A 5xx its route expects (a page asked for while the data is calculated) is no error of the span, while a 5xx
// answered without a Go error is one with the status as its message; a 5xx of an error keeps the error otelfiber
// records. The exception of a 5xx without an error has a type of its own, for Error Tracking to group it apart.
func TestServerSpanStatusOf5xx(t *testing.T) {
	app, recorder := newTracedServiceApp(t, 1)
	app.Get("/cold", func(c *fiber.Ctx) error {
		middlewares.ExpectStatus(c)
		return c.SendStatus(fiber.StatusServiceUnavailable)
	})
	app.Get("/unavailable", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusServiceUnavailable)
	})
	app.Get("/failed", func(c *fiber.Ctx) error {
		return fiber.NewError(fiber.StatusInternalServerError, "it failed")
	})

	for _, tt := range []struct {
		path          string
		code          codes.Code
		exception     string
		exceptionType string
		expected      string
	}{
		{"/cold", codes.Ok, "", "", "true"},
		{"/unavailable", codes.Error, "503 Service Unavailable", "exusiai.dev/backend-next/internal/pkg/middlewares.httpStatusError", ""},
		{"/failed", codes.Error, "it failed", "", ""},
	} {
		t.Run(tt.path, func(t *testing.T) {
			before := len(recorder.Ended())
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, tt.path, nil))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			spans := recorder.Ended()[before:]
			if len(spans) != 1 {
				t.Fatalf("recorded %d spans, want the server span", len(spans))
			}
			span := spans[0]
			if span.Status().Code != tt.code {
				t.Errorf("status %v, want %v", span.Status(), tt.code)
			}
			var exceptions, exceptionTypes []string
			for _, e := range span.Events() {
				if e.Name != "exception" {
					continue
				}
				for _, kv := range e.Attributes {
					switch kv.Key {
					case "exception.message":
						exceptions = append(exceptions, kv.Value.AsString())
					case "exception.type":
						exceptionTypes = append(exceptionTypes, kv.Value.AsString())
					}
				}
			}
			if tt.exceptionType != "" && (len(exceptionTypes) != 1 || exceptionTypes[0] != tt.exceptionType) {
				t.Errorf("exception types %q, want %q", exceptionTypes, tt.exceptionType)
			}
			if tt.exception == "" && len(exceptions) != 0 || tt.exception != "" && (len(exceptions) != 1 || exceptions[0] != tt.exception) {
				t.Errorf("exceptions %q, want %q", exceptions, tt.exception)
			}
			if got := spanAttributes(span)["http.expected_status"]; got != tt.expected {
				t.Errorf("http.expected_status = %q, want %q", got, tt.expected)
			}
		})
	}
}
