package httpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
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

// newCORSServiceApp builds the real middleware chain with the default allowlist and these routes:
//   - GET /stats answers 200
//   - GET /limited is behind a limiter like the /result one, which lets one request through
//   - GET /bare-429 answers 429 JSON without a Retry-After, like a hand-written rejection
//   - GET /penguin-429 returns a 429 PenguinError
func newCORSServiceApp(t *testing.T) *fiber.App {
	t.Helper()

	conf := &appconfig.Config{}
	conf.CORSCredentialedOrigins = defaultCredentialedOrigins(t)
	app := CreateServiceApp(conf)

	tooMany := func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"code":    "TOO_MANY_REQUESTS",
			"message": "Your client is sending requests too frequently.",
		})
	}
	app.Get("/stats", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	app.Get("/limited", limiter.New(limiter.Config{
		LimitReached: tooMany,
		Max:          1,
		Expiration:   time.Minute * 5,
	}), func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	app.Get("/bare-429", tooMany)
	app.Get("/penguin-429", func(c *fiber.Ctx) error {
		return pgerr.New(fiber.StatusTooManyRequests, "TOO_MANY_REQUESTS", "Your client is sending requests too frequently.")
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

func TestServiceAppTooManyRequestsCarryRetryAfterAndCORS(t *testing.T) {
	app := newCORSServiceApp(t)

	get := func(t *testing.T, path, origin string) *http.Response {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set(fiber.HeaderOrigin, origin)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	if resp := get(t, "/limited", "https://evil.example"); resp.StatusCode != fiber.StatusOK {
		t.Fatalf("first /limited: status = %d, want 200", resp.StatusCode)
	}

	tests := []struct {
		path       string
		origin     string
		retryAfter string // "" for any number of seconds but the 60 s fallback
	}{
		{"/limited", "https://evil.example", ""},
		{"/limited", "capacitor://localhost", ""},
		{"/bare-429", "https://evil.example", "60"},
		{"/penguin-429", "https://penguin-stats.io", "60"},
	}
	for _, tt := range tests {
		t.Run(tt.path+" "+tt.origin, func(t *testing.T) {
			resp := get(t, tt.path, tt.origin)
			if resp.StatusCode != fiber.StatusTooManyRequests {
				t.Fatalf("status = %d, want 429", resp.StatusCode)
			}

			retryAfter := resp.Header.Get(fiber.HeaderRetryAfter)
			if tt.retryAfter != "" && retryAfter != tt.retryAfter {
				t.Errorf("Retry-After = %q, want %q", retryAfter, tt.retryAfter)
			}
			if tt.retryAfter == "" && (retryAfter == "" || retryAfter == "60") {
				t.Errorf("Retry-After = %q, want the limiter's own", retryAfter)
			}

			var body struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			raw, _ := io.ReadAll(resp.Body)
			if err := json.Unmarshal(raw, &body); err != nil || body.Code != "TOO_MANY_REQUESTS" || body.Message == "" {
				t.Errorf("body = %s, want the JSON {code, message}", raw)
			}

			// a cross-origin client can read the 429 and its Retry-After
			assertCORS(t, resp, tt.origin, tt.origin != "https://evil.example")
		})
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
