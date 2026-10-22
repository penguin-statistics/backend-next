package middlewares

import (
	"github.com/gofiber/fiber/v2"
)

// apiLinks is the Link header (RFC 8288) of the API responses: the spec, the docs for humans, the license of the data
// and the API catalog (RFC 9727). The links are absolute: penguin-stats.cn passes only /PenguinStats/api/v2 to the
// backend, so a link relative to it would 404.
const apiLinks = `<https://penguin-stats.io/swagger/doc.json>; rel="service-desc"; type="application/json", ` +
	`<https://developer.penguin-stats.io/public-api>; rel="service-doc", ` +
	`<https://creativecommons.org/licenses/by-nc/4.0/>; rel="license", ` +
	`<https://penguin-stats.io/.well-known/api-catalog>; rel="api-catalog"`

// APIDiscovery links the API responses to the spec, the docs, the data license and the API catalog, so that a client
// (or an agent) that only sees a response finds out how to use the API and on which terms, and keeps search engines
// from indexing the raw JSON, which they may still crawl. It sets both headers before the handler runs, so that the
// error responses carry them too, and appends to a Link or X-Robots-Tag that is already set.
func APIDiscovery() fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Append(fiber.HeaderLink, apiLinks)
		c.Append(fiber.HeaderXRobotsTag, "noindex")
		return c.Next()
	}
}
