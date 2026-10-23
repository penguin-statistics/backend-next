package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/server/httpserver"
)

// cardApp returns an app serving the card of the server at endpoint.
func cardApp(t *testing.T, app *fiber.App, endpoint string) *fiber.App {
	t.Helper()
	u, err := parsePublicURL(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if app == nil {
		app = fiber.New()
	}
	if err := mountServerCard(app, newServerCard(u, site)); err != nil {
		t.Fatal(err)
	}
	return app
}

// request sends a request of method to path, with the headers given, and returns the response and its body.
func request(t *testing.T, app *fiber.App, method, path string, header ...string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

func TestServerCard(t *testing.T) {
	app := cardApp(t, nil, "https://mcp.penguin-stats.io/mcp")
	// whatever the client asks for, never a 406
	for _, accept := range []string{"application/mcp-server-card+json", "application/json", "*/*", ""} {
		resp, b := request(t, app, fiber.MethodGet, "/mcp/server-card", fiber.HeaderAccept, accept)
		if resp.StatusCode != fiber.StatusOK || resp.Header.Get(fiber.HeaderContentType) != "application/mcp-server-card+json" ||
			resp.Header.Get(fiber.HeaderCacheControl) != "public, max-age=3600" ||
			!regexp.MustCompile(`^W/"[0-9a-f]{16}"$`).MatchString(resp.Header.Get(fiber.HeaderETag)) {
			t.Fatalf("Accept %q: status %d, headers %v", accept, resp.StatusCode, resp.Header)
		}

		var card struct {
			Schema      string `json:"$schema"`
			Name        string `json:"name"`
			Version     string `json:"version"`
			Description string `json:"description"`
			Title       string `json:"title"`
			WebsiteURL  string `json:"websiteUrl"`
			Repository  struct {
				URL    string `json:"url"`
				Source string `json:"source"`
				ID     string `json:"id"`
			} `json:"repository"`
			Icons   []sdkmcp.Icon `json:"icons"`
			Remotes []struct {
				Type                      string   `json:"type"`
				URL                       string   `json:"url"`
				SupportedProtocolVersions []string `json:"supportedProtocolVersions"`
			} `json:"remotes"`
		}
		// a field this test does not know of fails it, so that every field is checked
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if err := d.Decode(&card); err != nil {
			t.Fatalf("%v: %s", err, b)
		}
		for _, f := range []struct{ field, got, want string }{
			{"$schema", card.Schema, "https://static.modelcontextprotocol.io/schemas/v1/server-card.schema.json"},
			{"name", card.Name, "io.penguin-stats/penguin-stats"},
			{"version", card.Version, listingVersion},
			{"description", card.Description, wantServerInfo.Description},
			{"title", card.Title, wantServerInfo.Title},
			{"websiteUrl", card.WebsiteURL, "https://mcp.penguin-stats.io/"},
			{"repository.url", card.Repository.URL, "https://github.com/penguin-statistics/backend-next"},
			{"repository.source", card.Repository.Source, "github"},
			{"repository.id", card.Repository.ID, "431197141"},
		} {
			if f.got != f.want {
				t.Errorf("%s %q, want %q", f.field, f.got, f.want)
			}
		}
		// what serverInfo has, and every protocol version the SDK serves (all of them, stateless)
		if !reflect.DeepEqual(card.Icons, wantServerInfo.Icons) {
			t.Errorf("icons %+v", card.Icons)
		}
		if len(card.Remotes) != 1 || card.Remotes[0].Type != "streamable-http" || card.Remotes[0].URL != "https://mcp.penguin-stats.io/mcp" ||
			!reflect.DeepEqual(card.Remotes[0].SupportedProtocolVersions, sdkmcp.SupportedProtocolVersions()) {
			t.Errorf("remotes %+v", card.Remotes)
		}
	}
}

// The card says what the listing in the MCP Registry says: checkers compare them.
func TestServerCardMatchesListing(t *testing.T) {
	b, err := os.ReadFile("../../../server.json")
	if err != nil {
		t.Fatal(err)
	}
	var listing map[string]any
	if err := json.Unmarshal(b, &listing); err != nil {
		t.Fatal(err)
	}
	_, b = request(t, cardApp(t, nil, publicURL), fiber.MethodGet, "/mcp/server-card")
	var card map[string]any
	if err := json.Unmarshal(b, &card); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"name", "version", "title", "description", "websiteUrl", "repository", "icons"} {
		if !reflect.DeepEqual(card[field], listing[field]) {
			t.Errorf("%s: card %v, server.json %v", field, card[field], listing[field])
		}
	}
	remote := card["remotes"].([]any)[0].(map[string]any)
	want := listing["remotes"].([]any)[0].(map[string]any)
	if remote["type"] != want["type"] || remote["url"] != want["url"] {
		t.Errorf("remote %v, server.json %v", remote, want)
	}
}

// A client revalidating the card it keeps is answered 304, with the weak tag or the same tag without W/.
func TestServerCardNotModified(t *testing.T) {
	app := cardApp(t, nil, "https://mcp.penguin-stats.io/mcp")
	resp, body := request(t, app, fiber.MethodGet, "/mcp/server-card")
	etag := resp.Header.Get(fiber.HeaderETag)
	strong := strings.TrimPrefix(etag, "W/")
	for ifNoneMatch, want := range map[string]int{
		etag:                      fiber.StatusNotModified,
		strong:                    fiber.StatusNotModified,
		`"x", ` + etag + ` `:      fiber.StatusNotModified,
		`"x",` + strong:           fiber.StatusNotModified,
		"*":                       fiber.StatusNotModified,
		`"0123456789abcdef"`:      fiber.StatusOK,
		strings.Trim(strong, `"`): fiber.StatusOK,
		`W/"0123456789abcdef", `:  fiber.StatusOK,
	} {
		for _, method := range []string{fiber.MethodGet, fiber.MethodHead} {
			resp, b := request(t, app, method, "/mcp/server-card", fiber.HeaderIfNoneMatch, ifNoneMatch)
			if resp.StatusCode != want || resp.Header.Get(fiber.HeaderETag) != etag || resp.Header.Get(fiber.HeaderCacheControl) != "public, max-age=3600" {
				t.Errorf("%s If-None-Match %q: status %d, want %d, headers %v", method, ifNoneMatch, resp.StatusCode, want, resp.Header)
			}
			if wantBody := want == fiber.StatusOK && method == fiber.MethodGet; wantBody != bytes.Equal(b, body) || !wantBody && len(b) != 0 {
				t.Errorf("%s If-None-Match %q: body %q", method, ifNoneMatch, b)
			}
		}
	}
}

func TestServerCardHead(t *testing.T) {
	app := cardApp(t, nil, "https://mcp.penguin-stats.io/mcp")
	getResp, body := request(t, app, fiber.MethodGet, "/mcp/server-card")
	resp, b := request(t, app, fiber.MethodHead, "/mcp/server-card")
	if resp.StatusCode != fiber.StatusOK || len(b) != 0 || resp.ContentLength != int64(len(body)) {
		t.Errorf("status %d, Content-Length %d (GET: %d bytes), body %q", resp.StatusCode, resp.ContentLength, len(body), b)
	}
	for _, h := range []string{fiber.HeaderContentType, fiber.HeaderCacheControl, fiber.HeaderETag} {
		if resp.Header.Get(h) != getResp.Header.Get(h) {
			t.Errorf("%s %q, GET %q", h, resp.Header.Get(h), getResp.Header.Get(h))
		}
	}
}

// Pages of any site may fetch the card and revalidate it, through the middleware chain of the service: the public tier
// of CORS, If-None-Match allowed and ETag exposed.
func TestServerCardCORS(t *testing.T) {
	conf := &appconfig.Config{}
	conf.CORSCredentialedOrigins = []string{"https://penguin-stats.io"}
	app := cardApp(t, httpserver.CreateServiceApp(conf), "https://mcp.penguin-stats.io/mcp")

	for _, origin := range []string{"https://penguin-stats.io", "https://example.org"} {
		resp, _ := request(t, app, fiber.MethodOptions, "/mcp/server-card", fiber.HeaderOrigin, origin,
			fiber.HeaderAccessControlRequestMethod, fiber.MethodGet, fiber.HeaderAccessControlRequestHeaders, "if-none-match")
		if resp.StatusCode != fiber.StatusNoContent || resp.Header.Get(fiber.HeaderAccessControlAllowOrigin) != "*" ||
			resp.Header.Get(fiber.HeaderAccessControlAllowCredentials) != "" ||
			resp.Header.Get(fiber.HeaderAccessControlAllowHeaders) != "if-none-match" ||
			!strings.Contains(resp.Header.Get(fiber.HeaderAccessControlAllowMethods), fiber.MethodGet) {
			t.Errorf("%s preflight: status %d, headers %v", origin, resp.StatusCode, resp.Header)
		}

		resp, _ = request(t, app, fiber.MethodGet, "/mcp/server-card", fiber.HeaderOrigin, origin)
		if resp.StatusCode != fiber.StatusOK || resp.Header.Get(fiber.HeaderAccessControlAllowOrigin) != "*" ||
			resp.Header.Get(fiber.HeaderAccessControlAllowCredentials) != "" ||
			!strings.Contains(resp.Header.Get(fiber.HeaderAccessControlExposeHeaders), fiber.HeaderETag) ||
			resp.Header.Get(fiber.HeaderSetCookie) != "" {
			t.Errorf("%s GET: status %d, headers %v", origin, resp.StatusCode, resp.Header)
		}
	}
}

// The card is not counted against the rate limit of /mcp: a client that used it up still gets the card.
func TestServerCardOutsideTheRateLimit(t *testing.T) {
	srv, _ := newServer(&tools{data: &fakeData{}, site: site}, "test")
	app := fiber.New(behindIngress)
	Mount(app, srv, 1, time.Minute)
	cardApp(t, app, "https://mcp.penguin-stats.io/mcp")

	const client = "X-Original-Forwarded-For"
	post(t, app, initialize, client, "203.0.113.7")
	if resp, b := post(t, app, initialize, client, "203.0.113.7"); resp.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	for i := 0; i < 3; i++ {
		if resp, b := request(t, app, fiber.MethodGet, "/mcp/server-card", client, "203.0.113.7"); resp.StatusCode != fiber.StatusOK {
			t.Errorf("card %d: status %d: %s", i, resp.StatusCode, b)
		}
	}
}

func TestParsePublicURL(t *testing.T) {
	for s, want := range map[string]string{
		"":                                 "",
		"https://mcp.penguin-stats.io/mcp": "mcp.penguin-stats.io",
		"https://localhost:8443/mcp":       "localhost:8443",
		"https://xn--n8j.example/mcp":      "xn--n8j.example",
	} {
		u, err := parsePublicURL(s)
		if err != nil {
			t.Errorf("%q: %v", s, err)
			continue
		}
		if (u == nil) != (want == "") || u != nil && (u.Host != want || u.String() != s) {
			t.Errorf("%q: %v", s, u)
		}
	}
	for _, s := range []string{
		"http://mcp.penguin-stats.io/mcp",
		"mcp.penguin-stats.io/mcp",
		"https://mcp.penguin-stats.io",
		"https://mcp.penguin-stats.io/",
		"https://mcp.penguin-stats.io/mcp/",
		"https://mcp.penguin-stats.io/MCP",
		"https://MCP.penguin-stats.io/mcp",
		"https://mcp.penguin-stats.io/mcp?via=x",
		"https://mcp.penguin-stats.io/mcp#x",
		"https://user@mcp.penguin-stats.io/mcp",
		"https://mcp.penguin-stats.io/v1/mcp",
		"https://-x.example/mcp",
		" https://mcp.penguin-stats.io/mcp",
	} {
		if u, err := parsePublicURL(s); err == nil || !strings.Contains(err.Error(), "PENGUIN_V3_MCP_PUBLIC_URL") {
			t.Errorf("%q: %v, %v", s, u, err)
		}
	}
	if u, _ := url.Parse("https://mcp.penguin-stats.io/mcp"); guideURL(u) != "https://mcp.penguin-stats.io/" {
		t.Errorf("guide %q", guideURL(u))
	}
}
