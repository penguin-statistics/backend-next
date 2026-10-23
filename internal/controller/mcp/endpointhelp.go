package mcp

import (
	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/pkg/middlewares"
)

// People open the URL of the endpoint, the one they are told to paste into their apps, in a browser, or curl it. A
// browser is sent to the guide, and another GET a 405 that says what the endpoint is and links the guide, instead of
// the bare 405 of the SDK (a GET of Streamable HTTP opens the stream of the server's messages, which a stateless
// server does not have). A GET listing text/event-stream, which MCP clients send and browsers and curl never do, keeps
// the 405 the specification asks for: the 2025-06-18 protocol has a server answer such a GET with a stream or 405,
// and the 2026-07-28 one, which drops the stream, has it answer an older client's with 405.

// mountEndpointHelp answers the GETs and HEADs of /mcp that are not for an event stream, ahead of the rate and
// concurrency limits, which do not concern them; the others go on to the server. guide is the URL of the guide.
func mountEndpointHelp(r fiber.Router, guide string) {
	explanation := "405: this is the MCP endpoint of Penguin Statistics. MCP clients POST JSON-RPC here (Streamable HTTP, " +
		"no authentication). Setup guide: " + guide + "\n"
	r.Get(middlewares.MCPPath, func(c *fiber.Ctx) error {
		q := acceptQ(c.Get(fiber.HeaderAccept))
		if q["text/event-stream"] > 0 {
			return c.Next()
		}
		c.Vary(fiber.HeaderAccept, "Sec-Fetch-Dest")
		c.Set(fiber.HeaderCacheControl, "no-store")
		c.Set(fiber.HeaderXRobotsTag, "noindex")
		// a page, or the browser loading one whatever it accepts
		if max(q["text/html"], q["application/xhtml+xml"], q["text/markdown"], q["text/x-markdown"]) > 0 ||
			c.Get("Sec-Fetch-Dest") == "document" {
			return c.Redirect(guide, fiber.StatusFound)
		}
		middlewares.ExpectStatus(c)
		c.Set(fiber.HeaderAllow, fiber.MethodPost)
		c.Set(fiber.HeaderLink, "<"+guide+`>; rel="help"`)
		c.Set(fiber.HeaderContentType, fiber.MIMETextPlainCharsetUTF8)
		return c.Status(fiber.StatusMethodNotAllowed).SendString(explanation)
	})
}
