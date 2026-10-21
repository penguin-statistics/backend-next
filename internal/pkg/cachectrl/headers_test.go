package cachectrl

import (
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
