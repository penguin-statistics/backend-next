package cachectrl

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestOptInCustomLeavesFreshnessToMaxAge(t *testing.T) {
	lastModifiedAt := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	app := fiber.New()
	app.Get("/", func(ctx *fiber.Ctx) error {
		OptInCustom(ctx, lastModifiedAt, time.Minute*5)
		return ctx.SendString("ok")
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("Cache-Control %q, want public, max-age=300", got)
	}
	if got := resp.Header.Get("Last-Modified"); got != "Tue, 20 Oct 2026 12:00:00 GMT" {
		t.Errorf("Last-Modified %q, want Tue, 20 Oct 2026 12:00:00 GMT", got)
	}
	if got := resp.Header.Values("Expires"); len(got) != 0 {
		t.Errorf("Expires %q, want none", got)
	}
}

func TestNotModified(t *testing.T) {
	// Last-Modified has a precision of seconds: the fraction must not make the response look newer than the header
	lastModified := time.Now().Add(-time.Hour).Truncate(time.Second).Add(500 * time.Millisecond)
	header := lastModified.UTC().Format(http.TimeFormat)

	app := fiber.New()
	send := func(ctx *fiber.Ctx) error {
		if NotModified(ctx) {
			return ctx.SendStatus(fiber.StatusNotModified)
		}
		return ctx.SendString("body")
	}
	app.Get("/global", func(ctx *fiber.Ctx) error {
		OptIn(ctx, lastModified)
		return send(ctx)
	})
	app.Get("/personal", func(ctx *fiber.Ctx) error {
		OptOut(ctx)
		return send(ctx)
	})
	app.Get("/uncached", send)

	tests := []struct {
		name    string
		path    string
		headers map[string]string
		want    int
	}{
		{"unconditional", "/global", nil, fiber.StatusOK},
		{"same as Last-Modified", "/global", map[string]string{fiber.HeaderIfModifiedSince: header}, fiber.StatusNotModified},
		{"after Last-Modified", "/global", map[string]string{fiber.HeaderIfModifiedSince: lastModified.Add(time.Minute).UTC().Format(http.TimeFormat)}, fiber.StatusNotModified},
		{"stale", "/global", map[string]string{fiber.HeaderIfModifiedSince: lastModified.Add(-time.Second).UTC().Format(http.TimeFormat)}, fiber.StatusOK},
		{"in the future", "/global", map[string]string{fiber.HeaderIfModifiedSince: time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)}, fiber.StatusOK},
		{"invalid", "/global", map[string]string{fiber.HeaderIfModifiedSince: "yesterday"}, fiber.StatusOK},
		{"with If-None-Match", "/global", map[string]string{fiber.HeaderIfModifiedSince: header, fiber.HeaderIfNoneMatch: `"x"`}, fiber.StatusOK},
		{"personal", "/personal", map[string]string{fiber.HeaderIfModifiedSince: header}, fiber.StatusOK},
		{"without Last-Modified", "/uncached", map[string]string{fiber.HeaderIfModifiedSince: header}, fiber.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(fiber.MethodGet, tt.path, nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			resp, err := app.Test(req, -1)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tt.want {
				t.Fatalf("status %d, want %d", resp.StatusCode, tt.want)
			}
			if tt.want == fiber.StatusNotModified {
				if len(body) != 0 {
					t.Errorf("304 with body %q", body)
				}
				// a 304 carries the headers the 200 would, which refresh the client's copy
				if got := resp.Header.Get(fiber.HeaderLastModified); got != header {
					t.Errorf("Last-Modified %q, want %q", got, header)
				}
				if got := resp.Header.Get(fiber.HeaderCacheControl); got != "public, max-age=300" {
					t.Errorf("Cache-Control %q", got)
				}
			} else if string(body) != "body" {
				t.Errorf("body %q", body)
			}
		})
	}
}
