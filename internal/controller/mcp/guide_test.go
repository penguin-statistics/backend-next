package mcp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx/fxtest"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/controller/mcp/guide"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/server/httpserver"
	"exusiai.dev/backend-next/internal/service"
)

// guideHost is the host of the guide in the tests, that of production
const guideHost = "https://mcp.penguin-stats.io"

// guideApp returns an app serving the MCP server and its guide at publicURL, as Register does.
func guideApp(t *testing.T, app *fiber.App) *fiber.App {
	t.Helper()
	if app == nil {
		app = fiber.New()
	}
	u, err := url.Parse(publicURL)
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := newServer(&tools{data: &fakeData{}, site: site}, "test")
	Mount(app, srv, 1000, time.Minute)
	if err := mountServerCard(app, newServerCard(u, site)); err != nil {
		t.Fatal(err)
	}
	if err := mountGuide(app, u, site, 120, time.Minute); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestNegotiateGuide(t *testing.T) {
	for accept, want := range map[string]guideFormat{
		"":                 guideHTML,
		"*/*":              guideHTML,
		"text/*":           guideHTML,
		"application/json": guideHTML,
		"text/html":        guideHTML,
		"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8": guideHTML,
		"text/markdown":                         guideMarkdown,
		"TEXT/Markdown; charset=utf-8":          guideMarkdown,
		"text/x-markdown":                       guideMarkdown,
		"text/markdown, text/html, */*":         guideMarkdown,
		"text/markdown, text/html;q=0.9":        guideMarkdown,
		"text/markdown;q=0.5, text/html;q=0.5":  guideMarkdown,
		"text/html, text/markdown;q=0.9":        guideHTML,
		"text/html;q=0, text/markdown;q=0.1":    guideMarkdown,
		"text/markdown;q=0":                     guideHTML,
		"text/markdown;q=x":                     guideHTML,
		"text/plain":                            guidePlain,
		"text/plain, */*;q=0.1":                 guidePlain,
		"text/plain, text/markdown;q=0.2":       guideMarkdown,
		"text/plain, text/html;q=0.2":           guideHTML,
		"text/event-stream":                     guideNotAcceptable,
		"application/json, text/event-stream":   guideNotAcceptable,
		"text/event-stream, */*":                guideNotAcceptable,
		"text/event-stream, text/html;q=0.1":    guideHTML,
		"text/event-stream;q=0, application/js": guideHTML,
	} {
		if got := negotiateGuide(accept); got != want {
			t.Errorf("%q: %d, want %d", accept, got, want)
		}
	}
}

func TestGuideLang(t *testing.T) {
	for _, tc := range []struct{ lang, acceptLanguage, want string }{
		{"", "", "en"},
		{"zh", "", "zh"},
		{"zh-CN", "", "zh"},
		{"zh-Hans", "en", "zh"},
		{"ZH-tw", "", "zh"},
		{"en", "zh-CN", "en"},
		// ja and ko are read in English until they are translated
		{"ja", "zh", "en"},
		{"ko", "", "en"},
		// a language the guide does not have is no choice
		{"fr", "zh", "zh"},
		{"", "zh-CN,zh;q=0.9,en;q=0.8", "zh"},
		{"", "en-US,en;q=0.9,zh-CN;q=0.8", "en"},
		{"", "zh-TW", "zh"},
		{"", "ja, zh;q=0.5", "en"},
		{"", "fr, zh;q=0.5", "zh"},
		{"", "fr", "en"},
		{"", "en;q=0, zh;q=0.1", "zh"},
		{"", "garbage;;", "en"},
	} {
		if got := guideLang(tc.lang, tc.acceptLanguage); got != tc.want {
			t.Errorf("lang %q, Accept-Language %q: %s, want %s", tc.lang, tc.acceptLanguage, got, tc.want)
		}
	}
}

// guideRequest sends a request of method to the path of the guide on host, with the headers given.
func guideRequest(t *testing.T, app *fiber.App, method, host, path string, header ...string) (*http.Response, []byte) {
	t.Helper()
	return request(t, app, method, host+path, header...)
}

func TestGuide(t *testing.T) {
	app := guideApp(t, nil)
	_, index := guideRequest(t, app, fiber.MethodGet, guideHost, "/index.md?lang=en")
	_, indexZH := guideRequest(t, app, fiber.MethodGet, guideHost, "/index.md?lang=zh")
	const (
		linkHTML = `<https://mcp.penguin-stats.io/index.md?lang=en>; rel="alternate"; type="text/markdown", ` +
			`<https://mcp.penguin-stats.io/llms.txt>; rel="describedby"; type="text/plain", ` +
			`<https://creativecommons.org/licenses/by-nc/4.0/>; rel="license", ` +
			`<https://mcp.penguin-stats.io/mcp/server-card>; rel="service-desc"; type="application/mcp-server-card+json"`
		linkMarkdown = `<https://mcp.penguin-stats.io/?lang=en>; rel="alternate"; type="text/html", ` +
			`<https://mcp.penguin-stats.io/llms.txt>; rel="describedby"; type="text/plain", ` +
			`<https://creativecommons.org/licenses/by-nc/4.0/>; rel="license", ` +
			`<https://mcp.penguin-stats.io/mcp/server-card>; rel="service-desc"; type="application/mcp-server-card+json"`
	)
	for _, tc := range []struct {
		name, method, path string
		header             []string
		status             int
		want               map[string]string
		body               func([]byte) bool
	}{
		{name: "browser", method: fiber.MethodGet, path: "/",
			header: []string{fiber.HeaderAccept, "text/html,application/xhtml+xml,*/*;q=0.8", fiber.HeaderAcceptLanguage, "en-US,en;q=0.9"},
			status: fiber.StatusOK, want: map[string]string{
				fiber.HeaderContentType: "text/html; charset=utf-8", fiber.HeaderContentLanguage: "en",
				fiber.HeaderVary: "Accept, Accept-Language", fiber.HeaderCacheControl: "private, max-age=300", fiber.HeaderLink: linkHTML,
				fiber.HeaderXContentTypeOptions: "nosniff", fiber.HeaderReferrerPolicy: "no-referrer",
			},
			body: func(b []byte) bool { return bytes.HasPrefix(b, []byte("<!doctype html>\n<html lang=\"en\"")) }},
		{name: "no Accept", method: fiber.MethodGet, path: "/", status: fiber.StatusOK,
			want: map[string]string{fiber.HeaderContentType: "text/html; charset=utf-8", fiber.HeaderContentLanguage: "en"}},
		{name: "browser in Chinese", method: fiber.MethodGet, path: "/", header: []string{fiber.HeaderAcceptLanguage, "zh-CN,zh;q=0.9,en;q=0.8"},
			status: fiber.StatusOK, want: map[string]string{fiber.HeaderContentType: "text/html; charset=utf-8", fiber.HeaderContentLanguage: "zh-CN"},
			body: func(b []byte) bool { return bytes.Contains(b, []byte(`<html lang="zh-CN"`)) }},
		{name: "?lang", method: fiber.MethodGet, path: "/?lang=zh", header: []string{fiber.HeaderAcceptLanguage, "en"},
			status: fiber.StatusOK, want: map[string]string{fiber.HeaderContentLanguage: "zh-CN"}},
		{name: "agent", method: fiber.MethodGet, path: "/", header: []string{fiber.HeaderAccept, "text/markdown, text/html;q=0.9"},
			status: fiber.StatusOK, want: map[string]string{
				fiber.HeaderContentType: "text/markdown; charset=utf-8", fiber.HeaderContentLanguage: "en",
				fiber.HeaderVary: "Accept, Accept-Language", fiber.HeaderCacheControl: "private, max-age=300", fiber.HeaderLink: linkMarkdown,
			},
			body: func(b []byte) bool { return bytes.Equal(b, index) }},
		{name: "agent in Chinese", method: fiber.MethodGet, path: "/", header: []string{fiber.HeaderAccept, "text/markdown", fiber.HeaderAcceptLanguage, "zh"},
			status: fiber.StatusOK, want: map[string]string{fiber.HeaderContentType: "text/markdown; charset=utf-8", fiber.HeaderContentLanguage: "zh-CN"},
			body: func(b []byte) bool { return bytes.Equal(b, indexZH) }},
		{name: "text only", method: fiber.MethodGet, path: "/", header: []string{fiber.HeaderAccept, "text/plain"},
			status: fiber.StatusOK, want: map[string]string{fiber.HeaderContentType: "text/plain; charset=utf-8"},
			body: func(b []byte) bool { return bytes.Equal(b, index) }},
		{name: "HEAD", method: fiber.MethodHead, path: "/", status: fiber.StatusOK,
			want: map[string]string{fiber.HeaderContentType: "text/html; charset=utf-8", fiber.HeaderLink: linkHTML},
			body: func(b []byte) bool { return len(b) == 0 }},
		// an MCP client given the URL of the guide
		{name: "event stream", method: fiber.MethodGet, path: "/", header: []string{fiber.HeaderAccept, "application/json, text/event-stream"},
			status: fiber.StatusNotAcceptable, want: map[string]string{fiber.HeaderContentType: "application/json", fiber.HeaderCacheControl: "no-store"},
			body: func(b []byte) bool {
				return string(b) == `{"jsonrpc":"2.0","id":null,"error":{"code":-32000,"message":"This is the setup guide. The MCP endpoint is https://mcp.penguin-stats.io/mcp"}}`
			}},
		{name: "POST", method: fiber.MethodPost, path: "/", status: fiber.StatusTemporaryRedirect,
			want: map[string]string{fiber.HeaderLocation: "https://mcp.penguin-stats.io/mcp", fiber.HeaderContentType: "application/json", fiber.HeaderCacheControl: "no-store"},
			body: func(b []byte) bool {
				return bytes.Contains(b, []byte(`"message":"This is the setup guide. The MCP endpoint is https://mcp.penguin-stats.io/mcp"`))
			}},
		// the Markdown of a language, whatever the request
		{name: "index.md", method: fiber.MethodGet, path: "/index.md", header: []string{fiber.HeaderAcceptLanguage, "zh"},
			status: fiber.StatusOK, want: map[string]string{
				fiber.HeaderContentType: "text/markdown; charset=utf-8", fiber.HeaderContentLanguage: "en", fiber.HeaderCacheControl: "public, max-age=3600",
				fiber.HeaderVary: "", fiber.HeaderLink: `<https://mcp.penguin-stats.io/?lang=en>; rel="canonical", <https://creativecommons.org/licenses/by-nc/4.0/>; rel="license", ` +
					`<https://mcp.penguin-stats.io/mcp/server-card>; rel="service-desc"; type="application/mcp-server-card+json"`,
			},
			body: func(b []byte) bool { return bytes.Equal(b, index) }},
		{name: "index.md in Chinese", method: fiber.MethodGet, path: "/index.md?lang=zh-TW", status: fiber.StatusOK,
			want: map[string]string{fiber.HeaderContentLanguage: "zh-CN"}, body: func(b []byte) bool { return bytes.Equal(b, indexZH) }},
		{name: "llms.txt", method: fiber.MethodGet, path: "/llms.txt", status: fiber.StatusOK,
			want: map[string]string{fiber.HeaderContentType: "text/plain; charset=utf-8", fiber.HeaderContentLanguage: "en", fiber.HeaderCacheControl: "public, max-age=3600"},
			body: func(b []byte) bool { return bytes.HasPrefix(b, []byte("# Penguin Statistics MCP Server\n\n> ")) }},
		{name: "robots.txt", method: fiber.MethodGet, path: "/robots.txt", status: fiber.StatusOK,
			want: map[string]string{fiber.HeaderContentType: "text/plain; charset=utf-8", fiber.HeaderCacheControl: "public, max-age=86400"},
			body: func(b []byte) bool { return bytes.Contains(b, []byte("\nDisallow: /mcp\n")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, b := guideRequest(t, app, tc.method, guideHost, tc.path, tc.header...)
			if resp.StatusCode != tc.status {
				t.Fatalf("status %d, want %d: %.200s", resp.StatusCode, tc.status, b)
			}
			for name, want := range tc.want {
				if got := strings.Join(resp.Header.Values(name), ", "); got != want {
					t.Errorf("%s %q, want %q", name, got, want)
				}
			}
			if tc.body != nil && !tc.body(b) {
				t.Errorf("body %.300s", b)
			}
			if c := resp.Header.Get(fiber.HeaderSetCookie); c != "" {
				t.Errorf("Set-Cookie %s", c)
			}
			if tc.status == fiber.StatusOK && !strings.HasPrefix(resp.Header.Get(fiber.HeaderETag), `W/"`) {
				t.Errorf("ETag %q", resp.Header.Get(fiber.HeaderETag))
			}
		})
	}

	// the HTML only has a CSP
	resp, _ := guideRequest(t, app, fiber.MethodGet, guideHost, "/")
	if csp := resp.Header.Get(fiber.HeaderContentSecurityPolicy); !strings.HasPrefix(csp, "default-src 'none'; style-src 'sha256-") {
		t.Errorf("CSP %q", csp)
	}
}

// A client revalidating a page it keeps is answered 304, with the weak tag or the same tag without W/.
func TestGuideNotModified(t *testing.T) {
	app := guideApp(t, nil)
	for _, tc := range []struct{ path, accept string }{{"/", ""}, {"/", "text/markdown"}, {"/", "text/plain"}, {"/index.md?lang=zh", ""}, {"/llms.txt", ""}, {"/robots.txt", ""}} {
		resp, _ := guideRequest(t, app, fiber.MethodGet, guideHost, tc.path, fiber.HeaderAccept, tc.accept)
		etag := resp.Header.Get(fiber.HeaderETag)
		for _, ifNoneMatch := range []string{etag, strings.TrimPrefix(etag, "W/")} {
			resp, b := guideRequest(t, app, fiber.MethodGet, guideHost, tc.path, fiber.HeaderAccept, tc.accept, fiber.HeaderIfNoneMatch, ifNoneMatch)
			if resp.StatusCode != fiber.StatusNotModified || len(b) != 0 || resp.Header.Get(fiber.HeaderETag) != etag {
				t.Errorf("%s %q, If-None-Match %s: status %d, ETag %q", tc.path, tc.accept, ifNoneMatch, resp.StatusCode, resp.Header.Get(fiber.HeaderETag))
			}
		}
	}
	// the variants differ, the Markdown sent as text/plain too
	html, _ := guideRequest(t, app, fiber.MethodGet, guideHost, "/")
	zh, _ := guideRequest(t, app, fiber.MethodGet, guideHost, "/?lang=zh")
	if html.Header.Get(fiber.HeaderETag) == zh.Header.Get(fiber.HeaderETag) {
		t.Error("the same ETag in two languages")
	}
	markdown, _ := guideRequest(t, app, fiber.MethodGet, guideHost, "/", fiber.HeaderAccept, "text/markdown")
	plain, _ := guideRequest(t, app, fiber.MethodGet, guideHost, "/", fiber.HeaderAccept, "text/plain")
	if markdown.Header.Get(fiber.HeaderETag) == plain.Header.Get(fiber.HeaderETag) {
		t.Error("the same ETag for the Markdown as text/markdown and as text/plain")
	}
}

// Other hosts the backend answers never show the guide: their root is not its own.
func TestGuideOnItsHostOnly(t *testing.T) {
	app := guideApp(t, nil)
	for _, path := range []string{"/", "/index.md", "/llms.txt", "/robots.txt"} {
		for _, method := range []string{fiber.MethodGet, fiber.MethodHead, fiber.MethodPost} {
			if method == fiber.MethodPost && path != "/" {
				continue
			}
			for _, host := range []string{"https://penguin-stats.io", "https://penguin-stats.cn", "http://localhost:9010"} {
				if resp, b := guideRequest(t, app, method, host, path); resp.StatusCode != fiber.StatusNotFound {
					t.Errorf("%s %s%s: status %d: %.100s", method, host, path, resp.StatusCode, b)
				}
			}
		}
	}
	// the host as clients may write it
	for _, host := range []string{"https://MCP.Penguin-Stats.io", "https://mcp.penguin-stats.io:443"} {
		if resp, _ := guideRequest(t, app, fiber.MethodGet, host, "/"); resp.StatusCode != fiber.StatusOK {
			t.Errorf("%s: status %d", host, resp.StatusCode)
		}
	}
}

// A client given the URL of the guide rather than the endpoint still works: it follows the 307 with its POST.
func TestGuideRedirectsClientsToTheEndpoint(t *testing.T) {
	app := guideApp(t, nil)
	client := &http.Client{Transport: fiberTransport{app}}
	req, err := http.NewRequest(fiber.MethodPost, guideHost+"/", strings.NewReader(initialize))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(fiber.HeaderAccept, "application/json, text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	_, _ = b.ReadFrom(resp.Body)
	if r := decode(t, b.Bytes()); resp.StatusCode != fiber.StatusOK || r.Error != nil || resp.Request.URL.String() != publicURL {
		t.Errorf("status %d at %s: %s", resp.StatusCode, resp.Request.URL, b.Bytes())
	}

	// one that does not follow it reads why, with the ID of its request
	for body, id := range map[string]any{"": nil, `{"jsonrpc":"2.0","id":"x-1","method":"tools/list"}`: "x-1"} {
		req := httptest.NewRequest(fiber.MethodPost, guideHost+"/", strings.NewReader(body))
		req.RequestURI = ""
		resp, err := app.Test(req, 5000)
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		resp.Body.Close()
		if r := decode(t, b.Bytes()); resp.StatusCode != fiber.StatusTemporaryRedirect || r.ID != id || r.Error == nil || r.Error.Code != -32000 {
			t.Errorf("%q: status %d: %s", body, resp.StatusCode, b.Bytes())
		}
	}
}

// The tools the guide lists are those of the server: a new tool fails this test until the guide tells of it.
func TestGuideListsTheTools(t *testing.T) {
	_, r := newServer(&tools{data: &fakeData{}, site: site}, "test")
	var names []string
	for name := range r.names {
		names = append(names, name)
	}
	listed := guide.Tools()
	sort.Strings(names)
	sort.Strings(listed)
	if !slices.Equal(listed, names) {
		t.Errorf("the guide lists %v, the server has %v", listed, names)
	}
}

// The guide links the server card where it is served.
func TestGuideLinksTheCard(t *testing.T) {
	app := guideApp(t, nil)
	resp, _ := guideRequest(t, app, fiber.MethodGet, guideHost, "/")
	var card string
	for _, link := range strings.Split(resp.Header.Get(fiber.HeaderLink), ", <") {
		if strings.Contains(link, `rel="service-desc"`) {
			card = strings.Trim(strings.SplitN(link, ">", 2)[0], "<")
		}
	}
	u, err := url.Parse(card)
	if err != nil || u.Path != middlewares.MCPServerCardPath {
		t.Fatalf("card %q", card)
	}
	if resp, _ := request(t, app, fiber.MethodGet, card); resp.StatusCode != fiber.StatusOK {
		t.Errorf("card: status %d", resp.StatusCode)
	}
}

// Through the middlewares of the service: the headers of the guide win over those of helmet, Vary keeps Origin, CORS
// answers every origin alike, and nothing sets a cookie.
func TestGuideThroughTheService(t *testing.T) {
	cache.Initialize()
	conf := &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{MCPEnabled: true, SEOSiteURL: site, MCPPublicURL: publicURL,
		CORSCredentialedOrigins: []string{"https://penguin-stats.io"}}}
	app := httpserver.CreateServiceApp(conf)
	data := service.NewSEOData(fxtest.NewLifecycle(t), &appconfig.Config{}, nil, nil, nil, nil)
	if err := Register(app, conf, data); err != nil {
		t.Fatal(err)
	}
	resp, b := guideRequest(t, app, fiber.MethodGet, guideHost, "/", fiber.HeaderOrigin, "https://example.org",
		fiber.HeaderAcceptEncoding, "gzip")
	if resp.StatusCode != fiber.StatusOK || resp.Header.Get(fiber.HeaderReferrerPolicy) != "no-referrer" ||
		// helmet's require-corp would block the icon from penguin-stats.io
		resp.Header.Get("Cross-Origin-Embedder-Policy") != "unsafe-none" ||
		!strings.HasPrefix(resp.Header.Get(fiber.HeaderContentSecurityPolicy), "default-src 'none'") || resp.Header.Get(fiber.HeaderSetCookie) != "" {
		t.Errorf("status %d, headers %v", resp.StatusCode, resp.Header)
	}
	vary := strings.Join(resp.Header.Values(fiber.HeaderVary), ", ")
	for _, h := range []string{fiber.HeaderOrigin, fiber.HeaderAccept, fiber.HeaderAcceptLanguage, fiber.HeaderAcceptEncoding} {
		if !slices.Contains(strings.Split(vary, ", "), h) {
			t.Errorf("Vary %q lacks %s", vary, h)
		}
	}
	if len(b) == 0 {
		t.Error("no body")
	}
	// nor at the endpoint, where a browser lands too
	if resp, _ := guideRequest(t, app, fiber.MethodGet, guideHost, "/mcp"); resp.Header.Get(fiber.HeaderSetCookie) != "" {
		t.Errorf("/mcp: Set-Cookie %s", resp.Header.Get(fiber.HeaderSetCookie))
	}

	// the paths CORS answers in its public tier are those of the guide: a credentialed origin gets *, as any other,
	// since a shared cache keeps one response per URL
	if want := []string{"/", guide.MarkdownPath, guide.LLMsPath, guide.RobotsPath}; !slices.Equal(middlewares.MCPGuidePaths, want) {
		t.Fatalf("middlewares.MCPGuidePaths = %v, want %v", middlewares.MCPGuidePaths, want)
	}
	for _, path := range middlewares.MCPGuidePaths {
		resp, _ := guideRequest(t, app, fiber.MethodGet, guideHost, path, fiber.HeaderOrigin, "https://penguin-stats.io")
		if resp.StatusCode != fiber.StatusOK || resp.Header.Get(fiber.HeaderAccessControlAllowOrigin) != "*" ||
			resp.Header.Get(fiber.HeaderAccessControlAllowCredentials) != "" {
			t.Errorf("%s: status %d, headers %v", path, resp.StatusCode, resp.Header)
		}
	}
	// and a browser client given the URL of the page may send it the headers of MCP, once redirected to the endpoint
	resp, _ = guideRequest(t, app, fiber.MethodOptions, guideHost, "/", fiber.HeaderOrigin, "https://inspector.example",
		fiber.HeaderAccessControlRequestMethod, fiber.MethodPost,
		fiber.HeaderAccessControlRequestHeaders, "content-type,mcp-protocol-version")
	if got := resp.Header.Get(fiber.HeaderAccessControlAllowHeaders); resp.StatusCode != fiber.StatusNoContent || got != "content-type,mcp-protocol-version" {
		t.Errorf("preflight: status %d, Access-Control-Allow-Headers %q", resp.StatusCode, got)
	}
}
