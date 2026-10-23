package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp"

	"exusiai.dev/backend-next/internal/app/appconfig"
	v2 "exusiai.dev/backend-next/internal/controller/v2"
	"exusiai.dev/backend-next/internal/pkg/cachectrl"
	"exusiai.dev/backend-next/internal/server/svr"
)

type compressTestElement struct {
	StageID  string `json:"stageId"`
	ItemID   string `json:"itemId"`
	Quantity int    `json:"quantity"`
	Times    int    `json:"times"`
}

// newCompressTestApp returns the service app with a route standing in for the cacheable global responses (/large)
// and one under the /result group of RegisterResult, behind its response cache (/PenguinStats/api/v2/result/test),
// and the JSON both send
func newCompressTestApp(t *testing.T) (app *fiber.App, payload []byte, resultCalls *int) {
	t.Helper()

	elements := make([]compressTestElement, 1000)
	for i := range elements {
		elements[i] = compressTestElement{
			StageID:  "main_01-07",
			ItemID:   strconv.Itoa(30011 + i%10),
			Quantity: i * 7,
			Times:    i * 13,
		}
	}
	payload, err := json.Marshal(elements)
	if err != nil {
		t.Fatal(err)
	}

	conf := &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{
		DevMode:                   true,
		TrustedProxies:            []string{"127.0.0.1"},
		HTTPServerShutdownTimeout: time.Second,
	}}
	app = CreateServiceApp(conf)

	lastModified := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	app.Get("/large", func(ctx *fiber.Ctx) error {
		cachectrl.OptIn(ctx, lastModified)
		return ctx.JSON(elements)
	})

	// the real /result middlewares; the route added after them runs behind them as the /result routes do, without
	// the services those need
	group, _, _ := svr.CreateEndpointGroups(app, conf)
	v2.RegisterResult(group, v2.Result{})
	resultCalls = new(int)
	group.Get("/result/test", func(ctx *fiber.Ctx) error {
		*resultCalls++
		return ctx.JSON(elements)
	})

	return app, payload, resultCalls
}

// get sends a GET with the Accept-Encoding and the other headers and returns the response with its body as sent
func get(t *testing.T, app *fiber.App, path, acceptEncoding string, headers ...string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(fiber.MethodGet, path, nil)
	req.Header.Set(fiber.HeaderOrigin, "https://penguin-stats.io")
	if acceptEncoding != "" {
		req.Header.Set(fiber.HeaderAcceptEncoding, acceptEncoding)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

// decode returns body decoded from the Content-Encoding of resp
func decode(t *testing.T, resp *http.Response, body []byte) []byte {
	t.Helper()
	var decoded []byte
	var err error
	switch encoding := resp.Header.Get(fiber.HeaderContentEncoding); encoding {
	case "":
		return body
	case "gzip":
		decoded, err = fasthttp.AppendGunzipBytes(nil, body)
	case "br":
		decoded, err = fasthttp.AppendUnbrotliBytes(nil, body)
	default:
		t.Fatalf("unexpected Content-Encoding %q", encoding)
	}
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

// representationHeaders returns the headers of resp that do not depend on the request, on the time or on the
// content coding
func representationHeaders(resp *http.Response) http.Header {
	h := resp.Header.Clone()
	for _, k := range []string{fiber.HeaderDate, fiber.HeaderContentLength, fiber.HeaderContentEncoding, fiber.HeaderVary, "X-Penguin-Request-Id"} {
		h.Del(k)
	}
	return h
}

// wantVary is the Vary of each response compression may encode, whether it encodes it or not
const wantVary = "Origin, Accept-Encoding"

func TestServiceAppCompressesByAcceptEncoding(t *testing.T) {
	app, payload, _ := newCompressTestApp(t)

	identity, identityBody := get(t, app, "/large", "")
	if identity.StatusCode != fiber.StatusOK {
		t.Fatalf("status %d, want 200", identity.StatusCode)
	}
	// as before: the body as the handler sent it, without a content coding
	if !bytes.Equal(identityBody, payload) {
		t.Fatalf("body without Accept-Encoding differs from the handler's JSON")
	}
	if got := identity.Header.Get(fiber.HeaderContentEncoding); got != "" {
		t.Errorf("Content-Encoding without Accept-Encoding %q, want none", got)
	}
	if got := identity.Header.Get(fiber.HeaderContentLength); got != strconv.Itoa(len(payload)) {
		t.Errorf("Content-Length without Accept-Encoding %s, want %d", got, len(payload))
	}
	// the identity response varies on Accept-Encoding as well, or a shared cache could send it to every client
	if got := identity.Header.Get(fiber.HeaderVary); got != wantVary {
		t.Errorf("Vary without Accept-Encoding %q, want %q", got, wantVary)
	}
	for _, k := range []string{fiber.HeaderLastModified, fiber.HeaderCacheControl, fiber.HeaderContentType, fiber.HeaderAccessControlAllowOrigin} {
		if identity.Header.Get(k) == "" {
			t.Errorf("no %s", k)
		}
	}

	tests := []struct {
		acceptEncoding string
		want           string
	}{
		{"identity", ""},
		{"gzip", "gzip"},
		{"br", "br"},
		{"gzip, deflate, br", "br"},
	}
	for _, tt := range tests {
		t.Run(tt.acceptEncoding, func(t *testing.T) {
			resp, body := get(t, app, "/large", tt.acceptEncoding)
			if resp.StatusCode != fiber.StatusOK {
				t.Fatalf("status %d, want 200", resp.StatusCode)
			}
			if got := resp.Header.Get(fiber.HeaderContentEncoding); got != tt.want {
				t.Fatalf("Content-Encoding %q, want %q", got, tt.want)
			}
			if tt.want != "" && len(body)*5 > len(payload) {
				t.Errorf("compressed to %d of %d bytes, want under a fifth", len(body), len(payload))
			}
			if got := resp.Header.Get(fiber.HeaderVary); got != wantVary {
				t.Errorf("Vary %q, want %q", got, wantVary)
			}
			if got := resp.Header.Get(fiber.HeaderContentLength); got != strconv.Itoa(len(body)) {
				t.Errorf("Content-Length %s, want %d", got, len(body))
			}
			if !bytes.Equal(decode(t, resp, body), payload) {
				t.Errorf("decoded body differs from the handler's JSON")
			}
			// the content coding is the only difference, Last-Modified and Cache-Control included
			want, got := representationHeaders(identity), representationHeaders(resp)
			if len(got) != len(want) {
				t.Errorf("headers %v, want %v", got, want)
			}
			for k := range want {
				if got.Get(k) != want.Get(k) {
					t.Errorf("%s %q, want %q", k, got.Get(k), want.Get(k))
				}
			}
		})
	}
}

// The response cache of RegisterResult runs inside compression, so it keeps the uncompressed body: a hit is encoded
// for the request that hits it, whatever the request that filled it accepted.
// WOFF2 fonts are compressed already: they are sent as they are, whatever the request accepts.
func TestServiceAppDoesNotCompressFonts(t *testing.T) {
	app, payload, _ := newCompressTestApp(t)
	app.Get("/fonts/a.woff2", func(ctx *fiber.Ctx) error {
		ctx.Set(fiber.HeaderContentType, "font/woff2")
		return ctx.Send(payload)
	})
	for _, acceptEncoding := range []string{"", "gzip", "br, gzip"} {
		resp, body := get(t, app, "/fonts/a.woff2", acceptEncoding)
		if resp.StatusCode != fiber.StatusOK || resp.Header.Get(fiber.HeaderContentEncoding) != "" || !bytes.Equal(body, payload) {
			t.Errorf("Accept-Encoding %q: status %d, Content-Encoding %q, %d bytes of %d", acceptEncoding, resp.StatusCode,
				resp.Header.Get(fiber.HeaderContentEncoding), len(body), len(payload))
		}
	}
}

func TestServiceAppCompressesResultCacheHits(t *testing.T) {
	app, payload, resultCalls := newCompressTestApp(t)

	sequences := map[string][]string{
		"/PenguinStats/api/v2/result/test?itemFilter=30012":  {"gzip", "", "br", "identity"},
		"/PenguinStats/api/v2/result/test?stageFilter=main":  {"", "gzip", "br"},
		"/PenguinStats/api/v2/result/test?itemFilter=30013a": {"br", "gzip", ""},
	}
	for path, encodings := range sequences {
		calls := *resultCalls
		for i, acceptEncoding := range encodings {
			resp, body := get(t, app, path, acceptEncoding)
			if resp.StatusCode != fiber.StatusOK {
				t.Fatalf("%s %q: status %d, want 200", path, acceptEncoding, resp.StatusCode)
			}
			wantCache := "hit"
			if i == 0 {
				wantCache = "miss"
			}
			if got := resp.Header.Get(constant.CacheHeader); got != wantCache {
				t.Errorf("%s %q: %s %q, want %q", path, acceptEncoding, constant.CacheHeader, got, wantCache)
			}
			wantEncoding := acceptEncoding
			if wantEncoding == "identity" {
				wantEncoding = ""
			}
			if got := resp.Header.Get(fiber.HeaderContentEncoding); got != wantEncoding {
				t.Errorf("%s %q: Content-Encoding %q, want %q", path, acceptEncoding, got, wantEncoding)
			}
			if got := resp.Header.Get(fiber.HeaderVary); got != wantVary {
				t.Errorf("%s %q: Vary %q, want %q", path, acceptEncoding, got, wantVary)
			}
			if !bytes.Equal(decode(t, resp, body), payload) {
				t.Errorf("%s %q: decoded body differs from the handler's JSON", path, acceptEncoding)
			}
		}
		if got := *resultCalls - calls; got != 1 {
			t.Errorf("%s: handler called %d times, want 1", path, got)
		}
	}
}
