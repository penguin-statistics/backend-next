package httpserver

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/app/appconfig"
)

// defaultCredentialedOrigins returns the CORSCredentialedOrigins default the service ships with.
func defaultCredentialedOrigins(t *testing.T) []string {
	t.Helper()
	field, ok := reflect.TypeOf(appconfig.ConfigSpec{}).FieldByName("CORSCredentialedOrigins")
	if !ok {
		t.Fatal("appconfig.ConfigSpec has no CORSCredentialedOrigins")
	}
	return strings.Split(field.Tag.Get("default"), ",")
}

// newCORSServiceApp builds the real middleware chain with the default allowlist and a GET /stats answering 200.
func newCORSServiceApp(t *testing.T) *fiber.App {
	t.Helper()

	conf := &appconfig.Config{}
	conf.CORSCredentialedOrigins = defaultCredentialedOrigins(t)
	app := CreateServiceApp(conf)
	app.Get("/stats", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func TestServiceAppCORS(t *testing.T) {
	app := newCORSServiceApp(t)

	tests := []struct {
		origin       string
		credentialed bool
	}{
		{"https://penguin-stats.io", true},
		{"https://penguin-stats.cn", true},
		{"capacitor://localhost", true},
		{"https://frontend-v2-8db.pages.dev", true},
		{"https://1b5f51bd.frontend-v2-8db.pages.dev", true},
		{"https://frontend-legacy.exusiai.dev", true},
		{"", false},
		{"https://evil.example", false},
		{"https://ark-nights.com", false},
		{"https://echo.exusiai.dev", false},
		{"http://localhost", false},
		{"http://127.0.0.1:18089", false},
		{"null", false},
	}
	for _, tt := range tests {
		for _, method := range []string{http.MethodGet, http.MethodOptions} {
			t.Run(method+" "+tt.origin, func(t *testing.T) {
				req := httptest.NewRequest(method, "/stats", nil)
				if tt.origin != "" {
					req.Header.Set(fiber.HeaderOrigin, tt.origin)
				}
				if method == http.MethodOptions {
					req.Header.Set(fiber.HeaderAccessControlRequestMethod, http.MethodPost)
					req.Header.Set(fiber.HeaderAccessControlRequestHeaders, "authorization,content-type")
				}
				resp, err := app.Test(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()

				wantStatus := fiber.StatusOK
				if method == http.MethodOptions {
					wantStatus = fiber.StatusNoContent
				}
				if resp.StatusCode != wantStatus {
					t.Errorf("status = %d, want %d", resp.StatusCode, wantStatus)
				}
				assertCORS(t, resp, tt.origin, tt.credentialed)
			})
		}
	}
}

func assertCORS(t *testing.T, resp *http.Response, origin string, credentialed bool) {
	t.Helper()

	wantACAO, wantACAC := "*", []string(nil)
	if credentialed {
		wantACAO, wantACAC = origin, []string{"true"}
	}
	if got := resp.Header.Get(fiber.HeaderAccessControlAllowOrigin); got != wantACAO {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, wantACAO)
	}
	if got := resp.Header.Values(fiber.HeaderAccessControlAllowCredentials); !reflect.DeepEqual(got, wantACAC) {
		t.Errorf("Access-Control-Allow-Credentials = %q, want %q", got, wantACAC)
	}
	if vary := resp.Header.Get(fiber.HeaderVary); !strings.Contains(vary, fiber.HeaderOrigin) {
		t.Errorf("Vary = %q, want it to hold Origin", vary)
	}

	if resp.Request != nil && resp.Request.Method == http.MethodOptions {
		if got := resp.Header.Get(fiber.HeaderAccessControlAllowHeaders); !strings.Contains(got, "Authorization") {
			t.Errorf("Access-Control-Allow-Headers = %q, want it to list Authorization", got)
		}
		if got := resp.Header.Get(fiber.HeaderAccessControlAllowMethods); got != "GET,POST,DELETE,OPTIONS" {
			t.Errorf("Access-Control-Allow-Methods = %q, want GET,POST,DELETE,OPTIONS", got)
		}
		return
	}
	exposed := resp.Header.Get(fiber.HeaderAccessControlExposeHeaders)
	for _, h := range []string{"ETag", "Link", "Retry-After", "X-Penguin-Set-PenguinID"} {
		if !strings.Contains(exposed, h) {
			t.Errorf("Access-Control-Expose-Headers = %q, want it to hold %s", exposed, h)
		}
	}
}
