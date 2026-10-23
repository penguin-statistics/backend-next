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
	"*",                          // ignored
	"null",                       // ignored
	"https://a.*.example.com",    // ignored
	"https://example.com/path",   // ignored
	"https://*.pages.dev",        // ignored: public suffix
	"https://*.github.io",        // ignored: public suffix
	"https://*.com",              // ignored: public suffix
	"https://*.vercel.app.",      // ignored: trailing dot
	"https://*.netlify.app:8443", // ignored: port
	"",                           // ignored
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
		// wildcards over public suffixes are ignored, also when written with a trailing dot or a port
		{"https://x.vercel.app.", false},
		{"https://x.vercel.app", false},
		{"https://x.netlify.app:8443", false},
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
	if got := resp.Header.Get(fiber.HeaderAccessControlMaxAge); got != "" {
		t.Errorf("Access-Control-Max-Age = %q on a simple request, want none", got)
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
			if got, want := resp.Header.Get(fiber.HeaderAccessControlAllowHeaders), "Content-Type,Authorization,X-Requested-With,X-Penguin-Variant,sentry-trace,traceparent,tracestate"; got != want {
				t.Errorf("Access-Control-Allow-Headers = %q, want %q", got, want)
			}
			if got := resp.Header.Get(fiber.HeaderAccessControlMaxAge); got != "600" {
				t.Errorf("Access-Control-Max-Age = %q, want 600", got)
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

func TestCORSMCPPathIsPublic(t *testing.T) {
	app := fiber.New()
	app.Use(CORS(testCredentialedOrigins))
	app.All(MCPPath, func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	app.Get(MCPServerCardPath, func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	for _, path := range MCPGuidePaths {
		app.All(path, func(c *fiber.Ctx) error {
			return c.SendString("ok")
		})
	}
	app.Get("/mcpx", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	const mcpHeaders = "content-type,mcp-protocol-version,mcp-method,mcp-name,mcp-param-server"
	for _, tc := range []struct {
		name, method, path, origin, requestHeaders string
		wantACAO, wantAllowHeaders                 string
		wantCredentials                            bool
	}{
		// an allow-listed origin gets the public tier on /mcp, and so does the path as fiber routes it
		{name: "credentialed origin POST", method: fiber.MethodPost, path: "/mcp", origin: "https://penguin-stats.io", wantACAO: "*"},
		{name: "upper case path", method: fiber.MethodPost, path: "/MCP", origin: "capacitor://localhost", wantACAO: "*"},
		{name: "trailing slash", method: fiber.MethodPost, path: "/mcp/", origin: "capacitor://localhost", wantACAO: "*"},
		{name: "foreign origin POST", method: fiber.MethodPost, path: "/mcp", origin: "https://evil.example", wantACAO: "*"},
		// the MCP headers, Mcp-Param-* included, are allowed as asked for
		{name: "preflight", method: fiber.MethodOptions, path: "/mcp", origin: "https://penguin-stats.io", requestHeaders: mcpHeaders,
			wantACAO: "*", wantAllowHeaders: mcpHeaders},
		{name: "preflight without request headers", method: fiber.MethodOptions, path: "/mcp", origin: "https://inspector.example",
			wantACAO: "*", wantAllowHeaders: "Content-Type,Authorization,X-Requested-With,X-Penguin-Variant,sentry-trace,traceparent,tracestate,Mcp-Protocol-Version,Mcp-Method,Mcp-Name"},
		// the server card too, whose preflights allow If-None-Match, to revalidate it
		{name: "server card", method: fiber.MethodGet, path: "/mcp/server-card", origin: "https://penguin-stats.io", wantACAO: "*"},
		{name: "server card preflight", method: fiber.MethodOptions, path: "/mcp/server-card", origin: "https://penguin-stats.io",
			requestHeaders: "if-none-match", wantACAO: "*", wantAllowHeaders: "if-none-match"},
		{name: "server card preflight without request headers", method: fiber.MethodOptions, path: "/mcp/server-card/", origin: "https://evil.example",
			wantACAO: "*", wantAllowHeaders: "Content-Type,Authorization,X-Requested-With,X-Penguin-Variant,sentry-trace,traceparent,tracestate,If-None-Match"},
		// and the guide, whose files are cached by their URL alone, and whose page an MCP client may be given
		{name: "guide page", method: fiber.MethodGet, path: "/", origin: "https://penguin-stats.io", wantACAO: "*"},
		{name: "llms.txt", method: fiber.MethodGet, path: "/llms.txt", origin: "https://penguin-stats.io", wantACAO: "*"},
		{name: "robots.txt", method: fiber.MethodGet, path: "/robots.txt", origin: "capacitor://localhost", wantACAO: "*"},
		{name: "guide Markdown", method: fiber.MethodGet, path: "/INDEX.md", origin: "https://penguin-stats.io", wantACAO: "*"},
		{name: "guide page preflight", method: fiber.MethodOptions, path: "/", origin: "https://inspector.example", requestHeaders: mcpHeaders,
			wantACAO: "*", wantAllowHeaders: mcpHeaders},
		{name: "guide file preflight without request headers", method: fiber.MethodOptions, path: "/llms.txt", origin: "https://evil.example",
			wantACAO: "*", wantAllowHeaders: "Content-Type,Authorization,X-Requested-With,X-Penguin-Variant,sentry-trace,traceparent,tracestate,If-None-Match"},
		// other paths keep the tiers and the fixed list
		{name: "other path", method: fiber.MethodGet, path: "/mcpx", origin: "https://penguin-stats.io", wantACAO: "https://penguin-stats.io", wantCredentials: true},
		{name: "other path preflight", method: fiber.MethodOptions, path: "/mcpx", origin: "https://evil.example", requestHeaders: mcpHeaders,
			wantACAO: "*", wantAllowHeaders: "Content-Type,Authorization,X-Requested-With,X-Penguin-Variant,sentry-trace,traceparent,tracestate"},
		{name: "other path under /mcp", method: fiber.MethodGet, path: "/mcp/other", origin: "https://penguin-stats.io", wantACAO: "https://penguin-stats.io",
			wantCredentials: true},
		{name: "other file at the root", method: fiber.MethodGet, path: "/llms-full.txt", origin: "https://penguin-stats.io", wantACAO: "https://penguin-stats.io",
			wantCredentials: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set(fiber.HeaderOrigin, tc.origin)
			if tc.method == fiber.MethodOptions {
				req.Header.Set(fiber.HeaderAccessControlRequestMethod, fiber.MethodPost)
			}
			if tc.requestHeaders != "" {
				req.Header.Set(fiber.HeaderAccessControlRequestHeaders, tc.requestHeaders)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if got := resp.Header.Get(fiber.HeaderAccessControlAllowOrigin); got != tc.wantACAO {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tc.wantACAO)
			}
			if got := resp.Header.Get(fiber.HeaderAccessControlAllowCredentials) == "true"; got != tc.wantCredentials {
				t.Errorf("Access-Control-Allow-Credentials = %v, want %v", got, tc.wantCredentials)
			}
			if got := resp.Header.Get(fiber.HeaderAccessControlAllowHeaders); got != tc.wantAllowHeaders {
				t.Errorf("Access-Control-Allow-Headers = %q, want %q", got, tc.wantAllowHeaders)
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
