package middlewares

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestAPIDiscoveryAppends(t *testing.T) {
	app := fiber.New()
	// set before APIDiscovery runs, like a middleware in front of it
	app.Use("/preset", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderLink, `<https://example.com/next>; rel="next"`)
		c.Set(fiber.HeaderXRobotsTag, "nosnippet")
		return c.Next()
	})
	app.Use(APIDiscovery())
	ok := func(c *fiber.Ctx) error { return c.SendString("ok") }
	app.Get("/plain", ok)
	app.Get("/preset", ok)
	// adds the same headers again, as the idempotency middleware does when it replays a saved response
	app.Get("/replayed", func(c *fiber.Ctx) error {
		c.Append(fiber.HeaderLink, apiLinks)
		c.Append(fiber.HeaderXRobotsTag, "noindex")
		return c.SendString("ok")
	})

	tests := []struct {
		path       string
		link       string
		robotsTags string
	}{
		{"/plain", apiLinks, "noindex"},
		{"/preset", `<https://example.com/next>; rel="next", ` + apiLinks, "nosnippet, noindex"},
		{"/replayed", apiLinks, "noindex"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, tt.path, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if got := resp.Header.Values(fiber.HeaderLink); len(got) != 1 || got[0] != tt.link {
				t.Errorf("Link = %q, want [%q]", got, tt.link)
			}
			if got := resp.Header.Values(fiber.HeaderXRobotsTag); len(got) != 1 || got[0] != tt.robotsTags {
				t.Errorf("X-Robots-Tag = %q, want [%q]", got, tt.robotsTags)
			}
		})
	}
}
