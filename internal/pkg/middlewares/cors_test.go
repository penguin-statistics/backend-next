package middlewares

import (
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/pkg/pgid"
)

var testCredentialedOrigins = []string{
	"https://penguin-stats.io",
	" https://Penguin-Stats.CN/ ", // normalized
	"capacitor://localhost",
	"https://*.frontend-v2-8db.pages.dev",
	"*",                        // ignored
	"null",                     // ignored
	"https://a.*.example.com",  // ignored
	"https://example.com/path", // ignored
	"https://*.pages.dev",      // ignored: public suffix
	"https://*.github.io",      // ignored: public suffix
	"https://*.com",            // ignored: public suffix
	"",                         // ignored
}

func newCORSApp() *fiber.App {
	app := fiber.New()
	app.Use(CORS(testCredentialedOrigins))
	app.Get("/stats", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func TestCORSOriginTiers(t *testing.T) {
	app := newCORSApp()

	tests := []struct {
		origin       string
		credentialed bool
	}{
		{"https://penguin-stats.io", true},
		{"https://penguin-stats.cn", true},
		{"capacitor://localhost", true},
		{"https://1b5f51bd.frontend-v2-8db.pages.dev", true},
		{"https://feat-x.frontend-v2-8db.pages.dev", true},

		// no Origin at all, e.g. MAA
		{"", false},
		{"https://evil.example", false},
		{"null", false},
		// the wildcard covers exactly one label in front of the domain, on that scheme, without a port
		{"https://frontend-v2-8db.pages.dev", false},
		{"https://a.b.frontend-v2-8db.pages.dev", false},
		{"https://.frontend-v2-8db.pages.dev", false},
		{"http://x.frontend-v2-8db.pages.dev", false},
		{"https://x.frontend-v2-8db.pages.dev:8443", false},
		{"https://evil-frontend-v2-8db.pages.dev", false},
		// exact entries need the same scheme, host and port
		{"http://penguin-stats.io", false},
		{"https://penguin-stats.io:8443", false},
		{"https://penguin-stats.io.evil.example", false},
		{"https://sub.penguin-stats.io", false},
		{"http://localhost", false},
		// browsers serialize origins in lowercase
		{"https://PENGUIN-STATS.IO", false},
	}
	for _, tt := range tests {
		for _, method := range []string{fiber.MethodGet, fiber.MethodOptions} {
			t.Run(method+" "+tt.origin, func(t *testing.T) {
				req := httptest.NewRequest(method, "/stats", nil)
				if tt.origin != "" {
					req.Header.Set(fiber.HeaderOrigin, tt.origin)
				}
				if method == fiber.MethodOptions {
					req.Header.Set(fiber.HeaderAccessControlRequestMethod, fiber.MethodGet)
					req.Header.Set(fiber.HeaderAccessControlRequestHeaders, "authorization")
				}
				resp, err := app.Test(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()

				wantACAO, wantACAC := "*", []string(nil)
				if tt.credentialed {
					wantACAO, wantACAC = tt.origin, []string{"true"}
				}
				if got := resp.Header.Get(fiber.HeaderAccessControlAllowOrigin); got != wantACAO {
					t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, wantACAO)
				}
				// not even "false": the header is absent outside the allowlist
				if got := resp.Header.Values(fiber.HeaderAccessControlAllowCredentials); !reflect.DeepEqual(got, wantACAC) {
					t.Errorf("Access-Control-Allow-Credentials = %q, want %q", got, wantACAC)
				}
				if !varies(resp.Header.Values(fiber.HeaderVary), fiber.HeaderOrigin) {
					t.Errorf("Vary = %q, want it to hold Origin", resp.Header.Values(fiber.HeaderVary))
				}
			})
		}
	}
}

func TestCORSSimpleRequestExposesHeaders(t *testing.T) {
	app := newCORSApp()

	req := httptest.NewRequest(fiber.MethodGet, "/stats", nil)
	req.Header.Set(fiber.HeaderOrigin, "https://evil.example")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("status = %d, want 200: a simple request reaches the handler", resp.StatusCode)
	}

	exposed := strings.Split(resp.Header.Get(fiber.HeaderAccessControlExposeHeaders), ",")
	for _, h := range []string{
		"Content-Type", "ETag", "Link", "Retry-After",
		"X-Penguin-Set-PenguinID", "X-Penguin-Upgrade", "X-Penguin-Compatible", "X-Penguin-Request-ID",
	} {
		if !contains(exposed, h) {
			t.Errorf("Access-Control-Expose-Headers = %q, want it to hold %s", exposed, h)
		}
	}
	if got := resp.Header.Get(fiber.HeaderAccessControlAllowMethods); got != "" {
		t.Errorf("Access-Control-Allow-Methods = %q on a simple request, want none", got)
	}
}

func TestCORSPreflight(t *testing.T) {
	app := newCORSApp()

	for _, origin := range []string{"capacitor://localhost", "https://evil.example"} {
		t.Run(origin, func(t *testing.T) {
			req := httptest.NewRequest(fiber.MethodOptions, "/stats", nil)
			req.Header.Set(fiber.HeaderOrigin, origin)
			req.Header.Set(fiber.HeaderAccessControlRequestMethod, fiber.MethodPost)
			req.Header.Set(fiber.HeaderAccessControlRequestHeaders, "content-type,authorization")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != fiber.StatusNoContent {
				t.Errorf("status = %d, want 204", resp.StatusCode)
			}
			if got, want := resp.Header.Get(fiber.HeaderAccessControlAllowMethods), "GET,POST,DELETE,OPTIONS"; got != want {
				t.Errorf("Access-Control-Allow-Methods = %q, want %q", got, want)
			}
			// Authorization must be listed by name: a "*" in Allow-Headers does not cover it
			if got, want := resp.Header.Get(fiber.HeaderAccessControlAllowHeaders), "Content-Type,Authorization,X-Requested-With,X-Penguin-Variant,sentry-trace"; got != want {
				t.Errorf("Access-Control-Allow-Headers = %q, want %q", got, want)
			}
			vary := resp.Header.Values(fiber.HeaderVary)
			for _, h := range []string{fiber.HeaderOrigin, fiber.HeaderAccessControlRequestMethod, fiber.HeaderAccessControlRequestHeaders} {
				if !varies(vary, h) {
					t.Errorf("Vary = %q, want it to hold %s", vary, h)
				}
			}
		})
	}
}

func TestCORSIgnoresCookieOfCrossSiteRequests(t *testing.T) {
	app := fiber.New()
	app.Use(CORS(testCredentialedOrigins))
	app.Post("/report", func(c *fiber.Ctx) error {
		if pgid.IgnoredCookie(c) {
			return c.SendString("ignored")
		}
		return c.SendString("used")
	})

	tests := []struct {
		name         string
		origin       string
		secFetchSite string
		wantIgnored  bool
	}{
		{"allowlisted cross-site", "capacitor://localhost", "cross-site", false},
		{"allowlisted without fetch metadata", "https://1b5f51bd.frontend-v2-8db.pages.dev", "", false},
		{"foreign form", "https://evil.example", "cross-site", true},
		{"foreign without fetch metadata", "https://evil.example", "", true},
		{"null origin", "null", "cross-site", true},
		{"null origin without fetch metadata", "null", "", true},
		{"same origin, host not allowlisted", "https://mirror.example", "same-origin", false},
		{"same host without fetch metadata", "https://example.com", "", false},
		{"same site", "https://sub.example.com", "same-site", false},
		{"no Origin, e.g. MAA", "", "", false},
		{"cross-site navigation", "", "cross-site", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(fiber.MethodPost, "/report", strings.NewReader("server=CN"))
			req.Host = "example.com"
			req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationForm)
			req.Header.Set("Cookie", "userID=11111111")
			if tt.origin != "" {
				req.Header.Set(fiber.HeaderOrigin, tt.origin)
			}
			if tt.secFetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tt.secFetchSite)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			if got := string(body) == "ignored"; got != tt.wantIgnored {
				t.Errorf("cookie ignored = %v, want %v", got, tt.wantIgnored)
			}
		})
	}
}

func TestOriginAllowlistIgnoresInvalidEntries(t *testing.T) {
	l := newOriginAllowlist(testCredentialedOrigins)
	if len(l.exact) != 3 || len(l.subdomains) != 1 {
		t.Errorf("allowlist = %+v, want 3 exact origins and 1 wildcard", l)
	}
	for _, origin := range []string{
		"*", "null", "https://example.com", "https://example.com/path", "https://a.x.example.com",
		"https://evil.pages.dev", "https://evil.github.io", "https://evil.com",
	} {
		if l.allows(origin) {
			t.Errorf("allows(%q) = true, want false", origin)
		}
	}
}

func varies(values []string, header string) bool {
	for _, v := range values {
		for _, h := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(h), header) {
				return true
			}
		}
	}
	return false
}

func contains(values []string, s string) bool {
	for _, v := range values {
		if strings.TrimSpace(v) == s {
			return true
		}
	}
	return false
}
