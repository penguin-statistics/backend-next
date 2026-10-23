package mcp

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx/fxtest"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/service"
)

const helpText = "405: this is the MCP endpoint of Penguin Statistics. MCP clients POST JSON-RPC here (Streamable HTTP, " +
	"no authentication). Setup guide: https://mcp.penguin-stats.io/\n"

// helpApp returns an app serving the server behind a rate limit of limit requests a minute, with the help of /mcp, as
// Register does.
func helpApp(limit int) *fiber.App {
	srv, _ := newServer(&tools{data: &fakeData{}, site: site}, "test")
	app := fiber.New(behindIngress)
	mountEndpointHelp(app, "https://mcp.penguin-stats.io/")
	Mount(app, srv, limit, time.Minute)
	return app
}

func TestEndpointHelp(t *testing.T) {
	app := helpApp(1000)
	const browser = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8"
	for _, tc := range []struct {
		name, method string
		header       []string
		status       int
	}{
		{"browser", fiber.MethodGet, []string{fiber.HeaderAccept, browser}, fiber.StatusFound},
		{"browser HEAD", fiber.MethodHead, []string{fiber.HeaderAccept, browser}, fiber.StatusFound},
		{"agent", fiber.MethodGet, []string{fiber.HeaderAccept, "text/markdown, */*;q=0.1"}, fiber.StatusFound},
		{"navigation", fiber.MethodGet, []string{fiber.HeaderAccept, "*/*", "Sec-Fetch-Dest", "document"}, fiber.StatusFound},
		{"curl", fiber.MethodGet, []string{fiber.HeaderAccept, "*/*"}, fiber.StatusMethodNotAllowed},
		{"no Accept", fiber.MethodGet, nil, fiber.StatusMethodNotAllowed},
		{"curl HEAD", fiber.MethodHead, []string{fiber.HeaderAccept, "*/*"}, fiber.StatusMethodNotAllowed},
		{"fetch", fiber.MethodGet, []string{fiber.HeaderAccept, "application/json", "Sec-Fetch-Dest", "empty"}, fiber.StatusMethodNotAllowed},
		{"html refused", fiber.MethodGet, []string{fiber.HeaderAccept, "text/html;q=0, */*"}, fiber.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, b := request(t, app, tc.method, "/mcp", tc.header...)
			if resp.StatusCode != tc.status {
				t.Fatalf("status %d, want %d: %s", resp.StatusCode, tc.status, b)
			}
			vary := strings.Join(resp.Header.Values(fiber.HeaderVary), ", ")
			if !strings.Contains(vary, fiber.HeaderAccept) || !strings.Contains(vary, "Sec-Fetch-Dest") ||
				resp.Header.Get(fiber.HeaderCacheControl) != "no-store" || resp.Header.Get(fiber.HeaderXRobotsTag) != "noindex" {
				t.Errorf("headers %v", resp.Header)
			}
			switch tc.status {
			case fiber.StatusFound:
				if resp.Header.Get(fiber.HeaderLocation) != "https://mcp.penguin-stats.io/" {
					t.Errorf("Location %q", resp.Header.Get(fiber.HeaderLocation))
				}
			case fiber.StatusMethodNotAllowed:
				if resp.Header.Get(fiber.HeaderAllow) != fiber.MethodPost || resp.Header.Get(fiber.HeaderContentType) != "text/plain; charset=utf-8" ||
					resp.Header.Get(fiber.HeaderLink) != `<https://mcp.penguin-stats.io/>; rel="help"` {
					t.Errorf("headers %v", resp.Header)
				}
				if want := helpText; tc.method == fiber.MethodHead && len(b) != 0 || tc.method == fiber.MethodGet && string(b) != want {
					t.Errorf("body %q", b)
				}
			}
		})
	}

	// MCP clients keep the 405 of the SDK the specification asks for, and DELETE (the end of a session) too
	for _, tc := range []struct{ method, accept string }{
		{fiber.MethodGet, "text/event-stream"},
		{fiber.MethodGet, "application/json, text/event-stream"},
		{fiber.MethodGet, "text/event-stream, text/html"},
		{fiber.MethodHead, "text/event-stream"},
		{fiber.MethodDelete, "*/*"},
	} {
		resp, b := request(t, app, tc.method, "/mcp", fiber.HeaderAccept, tc.accept)
		if resp.StatusCode != fiber.StatusMethodNotAllowed || resp.Header.Get(fiber.HeaderAllow) != fiber.MethodPost ||
			resp.Header.Get(fiber.HeaderLink) != "" || bytes.Contains(b, []byte("Setup guide")) {
			t.Errorf("%s %q: status %d, headers %v: %s", tc.method, tc.accept, resp.StatusCode, resp.Header, b)
		}
	}
	// and POST is the server's
	if resp, b := post(t, app, initialize); resp.StatusCode != fiber.StatusOK {
		t.Errorf("POST: status %d: %s", resp.StatusCode, b)
	}
}

// A browser opening /mcp is helped also when the rate limit was used up: it does not count against it.
func TestEndpointHelpIsAheadOfTheLimits(t *testing.T) {
	app := helpApp(1)
	const client = "X-Original-Forwarded-For"
	post(t, app, initialize, client, "203.0.113.7")
	if resp, b := post(t, app, initialize, client, "203.0.113.7"); resp.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	for accept, want := range map[string]int{"text/html": fiber.StatusFound, "*/*": fiber.StatusMethodNotAllowed} {
		for i := 0; i < 3; i++ {
			if resp, b := request(t, app, fiber.MethodGet, "/mcp", fiber.HeaderAccept, accept, client, "203.0.113.7"); resp.StatusCode != want {
				t.Errorf("%s %d: status %d, want %d: %s", accept, i, resp.StatusCode, want, b)
			}
		}
	}
}

// Without a public URL, there is no guide to send browsers to: /mcp answers as the SDK does.
func TestEndpointHelpNeedsAPublicURL(t *testing.T) {
	cache.Initialize()
	for u, want := range map[string]int{"": fiber.StatusMethodNotAllowed, publicURL: fiber.StatusFound} {
		data := service.NewSEOData(fxtest.NewLifecycle(t), &appconfig.Config{}, nil, nil, nil, nil)
		app := fiber.New()
		if err := Register(app, &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{MCPEnabled: true, SEOSiteURL: site, MCPPublicURL: u}}, data); err != nil {
			t.Fatal(err)
		}
		resp, b := request(t, app, fiber.MethodGet, "/mcp", fiber.HeaderAccept, "text/html")
		if resp.StatusCode != want {
			t.Errorf("public URL %q: status %d, want %d: %s", u, resp.StatusCode, want, b)
		}
		if u == "" && (resp.Header.Get(fiber.HeaderLink) != "" || bytes.Contains(b, []byte("Setup guide"))) {
			t.Errorf("no public URL: headers %v: %s", resp.Header, b)
		}
	}
}

// The requests of the plugins are counted by mcp.via, and no other value of ?via is logged.
func TestViaPlugin(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = prev })

	srv, _ := newServer(&tools{data: &fakeData{}, site: site}, "test")
	app := fiber.New()
	middlewares.Logger(app)
	Mount(app, srv, 1000, time.Minute)
	for query, want := range map[string]any{"?via=plugin": "plugin", "?via=other": nil, "?via=plugin2": nil, "": nil} {
		buf.Reset()
		req := httptest.NewRequest(fiber.MethodPost, "/mcp"+query, strings.NewReader(initialize))
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		req.Header.Set(fiber.HeaderAccept, "application/json, text/event-stream")
		resp, err := app.Test(req, 5000)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		line := requestLogLine(t, &buf)
		if resp.StatusCode != fiber.StatusOK || line["mcp.via"] != want || line["mcp.method"] != "initialize" {
			t.Errorf("%q: status %d, mcp.via %v", query, resp.StatusCode, line["mcp.via"])
		}
	}
}
