package httpserver

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	"github.com/pkg/errors"

	"exusiai.dev/backend-next/internal/app/appconfig"
	v2 "exusiai.dev/backend-next/internal/controller/v2"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/server/svr"
)

const wantAPILinks = `<https://penguin-stats.io/swagger/doc.json>; rel="service-desc"; type="application/json", ` +
	`<https://developer.penguin-stats.io/public-api>; rel="service-doc", ` +
	`<https://creativecommons.org/licenses/by-nc/4.0/>; rel="license", ` +
	`<https://penguin-stats.io/.well-known/api-catalog>; rel="api-catalog"`

// Every response under /PenguinStats and /api carries the Link and X-Robots-Tag headers of APIDiscovery, the error
// responses, cache hits and the 404 of a path no route matches included; the stats pages, sitemaps, /swagger and /mcp
// do not.
func TestServiceAppLinksAPIResponses(t *testing.T) {
	conf := &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{
		DevMode:                   true,
		AdminKey:                  strings.Repeat("k", 64),
		HTTPServerShutdownTimeout: time.Second,
	}}
	app := CreateServiceApp(conf)
	group, admin, meta := svr.CreateEndpointGroups(app, conf)
	ok := func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) }
	group.Get("/stages", ok)
	group.Get("/invalid", func(c *fiber.Ctx) error { return pgerr.ErrInvalidReq })
	group.Get("/boom", func(c *fiber.Ctx) error { return errors.New("boom") })
	group.Get("/panic", func(c *fiber.Ctx) error { panic("boom") })
	// behind the real response cache of the /result group, which answers a hit without calling the handler
	v2.RegisterResult(group, v2.Result{})
	group.Get("/result/test", ok)
	admin.Get("/bonjour", ok)
	meta.Get("/ping", ok)
	// registered on the app itself, like meta.RegisterIndex
	app.Get("/api", ok)
	// not the API
	app.Get("/mcp", ok)
	app.Post("/mcp", ok)
	app.Get("/en/result/*", ok)
	app.Get("/sitemap.xml", ok)
	app.Get("/swagger/*", ok)

	tests := []struct {
		method string
		path   string
		status int
		// the X-Penguin-Cache header of the /result response cache, if any
		cache string
		api   bool
	}{
		{fiber.MethodGet, "/PenguinStats/api/v2/stages", fiber.StatusOK, "", true},
		{fiber.MethodHead, "/PenguinStats/api/v2/stages", fiber.StatusOK, "", true},
		{fiber.MethodGet, "/PenguinStats/api/v2/invalid", fiber.StatusBadRequest, "", true},
		{fiber.MethodGet, "/PenguinStats/api/v2/boom", fiber.StatusInternalServerError, "", true},
		{fiber.MethodGet, "/PenguinStats/api/v2/panic", fiber.StatusInternalServerError, "", true},
		{fiber.MethodGet, "/PenguinStats/api/v2/result/test?itemFilter=30012", fiber.StatusOK, "miss", true},
		{fiber.MethodGet, "/PenguinStats/api/v2/result/test?itemFilter=30012", fiber.StatusOK, "hit", true},
		{fiber.MethodGet, "/PenguinStats/api/v2/no-such-route", fiber.StatusNotFound, "", true},
		{fiber.MethodGet, "/PenguinStats/no-such-route", fiber.StatusNotFound, "", true},
		{fiber.MethodGet, "/api/admin/bonjour", fiber.StatusUnauthorized, "", true},
		{fiber.MethodGet, "/api/_/ping", fiber.StatusOK, "", true},
		{fiber.MethodGet, "/api", fiber.StatusOK, "", true},
		{fiber.MethodGet, "/mcp", fiber.StatusOK, "", false},
		{fiber.MethodPost, "/mcp", fiber.StatusOK, "", false},
		{fiber.MethodGet, "/en/result/cn/item/30012", fiber.StatusOK, "", false},
		{fiber.MethodGet, "/sitemap.xml", fiber.StatusOK, "", false},
		{fiber.MethodGet, "/swagger/doc.json", fiber.StatusOK, "", false},
		{fiber.MethodGet, "/no-such-route", fiber.StatusNotFound, "", false},
	}
	for _, tt := range tests {
		resp, err := app.Test(httptest.NewRequest(tt.method, tt.path, nil), -1)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		name := tt.method + " " + tt.path
		if tt.cache != "" {
			name += " (" + tt.cache + ")"
			if got := resp.Header.Get(constant.CacheHeader); got != tt.cache {
				t.Errorf("%s: %s = %q, want %q", name, constant.CacheHeader, got, tt.cache)
			}
		}
		if resp.StatusCode != tt.status {
			t.Errorf("%s: status = %d, want %d", name, resp.StatusCode, tt.status)
		}
		wantLink, wantRobots := "", ""
		if tt.api {
			wantLink, wantRobots = wantAPILinks, "noindex"
		}
		if got := resp.Header.Get(fiber.HeaderLink); got != wantLink {
			t.Errorf("%s: Link = %q, want %q", name, got, wantLink)
		}
		if got := resp.Header.Get(fiber.HeaderXRobotsTag); got != wantRobots {
			t.Errorf("%s: X-Robots-Tag = %q, want %q", name, got, wantRobots)
		}
	}
}
