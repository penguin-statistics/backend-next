package middlewares

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

var (
	corsAllowMethods = strings.Join([]string{
		fiber.MethodGet, fiber.MethodPost, fiber.MethodDelete, fiber.MethodOptions,
	}, ",")
	corsAllowHeaders = strings.Join([]string{
		fiber.HeaderContentType, fiber.HeaderAuthorization, fiber.HeaderXRequestedWith, "X-Penguin-Variant", "sentry-trace",
	}, ",")
	// response headers outside the CORS-safelisted set must be listed here for cross-origin scripts to read them
	corsExposeHeaders = strings.Join([]string{
		fiber.HeaderContentType, fiber.HeaderETag, fiber.HeaderLink, fiber.HeaderRetryAfter,
		"X-Penguin-Set-PenguinID", "X-Penguin-Upgrade", "X-Penguin-Compatible", "X-Penguin-Request-ID",
	}, ",")
)

// CORS answers cross-origin requests in two tiers.
//
// A request whose Origin is in credentialedOrigins gets that origin back in Access-Control-Allow-Origin, together
// with Access-Control-Allow-Credentials: true, so the browser may send the PenguinID cookie and let the page read the
// response. Every other request, including one without an Origin, gets Access-Control-Allow-Origin: * and no
// Access-Control-Allow-Credentials: any site may still read public data, but never with the visitor's credentials.
//
// Each entry of credentialedOrigins is either an exact origin ("https://penguin-stats.io", "capacitor://localhost";
// scheme, host and port must all match) or "<scheme>://*.<domain>", which matches the origins of <scheme> whose host
// is one more DNS label in front of <domain> (not <domain> itself, nor a deeper subdomain, nor any port). Entries are
// lowercased; "*", "null" and malformed entries are ignored with a warning.
//
// Every OPTIONS request is answered here as a preflight with 204, for both tiers.
func CORS(credentialedOrigins []string) fiber.Handler {
	allowlist := newOriginAllowlist(credentialedOrigins)

	return func(c *fiber.Ctx) error {
		origin := c.Get(fiber.HeaderOrigin)

		// the answer depends on Origin, whichever tier it falls in
		c.Vary(fiber.HeaderOrigin)
		if allowlist.allows(origin) {
			c.Set(fiber.HeaderAccessControlAllowOrigin, origin)
			c.Set(fiber.HeaderAccessControlAllowCredentials, "true")
		} else {
			c.Set(fiber.HeaderAccessControlAllowOrigin, "*")
		}

		if c.Method() != fiber.MethodOptions {
			c.Set(fiber.HeaderAccessControlExposeHeaders, corsExposeHeaders)
			return c.Next()
		}

		c.Vary(fiber.HeaderAccessControlRequestMethod)
		c.Vary(fiber.HeaderAccessControlRequestHeaders)
		c.Set(fiber.HeaderAccessControlAllowMethods, corsAllowMethods)
		c.Set(fiber.HeaderAccessControlAllowHeaders, corsAllowHeaders)
		return c.SendStatus(fiber.StatusNoContent)
	}
}

type subdomainOrigin struct {
	// prefix is "<scheme>://"
	prefix string
	// suffix is ".<domain>"
	suffix string
}

type originAllowlist struct {
	exact      map[string]struct{}
	subdomains []subdomainOrigin
}

func newOriginAllowlist(entries []string) originAllowlist {
	l := originAllowlist{exact: make(map[string]struct{})}
	accepted := make([]string, 0, len(entries))
	for _, entry := range entries {
		o := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(entry)), "/")
		if o == "" {
			continue
		}

		scheme, host, ok := strings.Cut(o, "://")
		if !ok || scheme == "" || host == "" || strings.ContainsAny(host, "/?#@") {
			log.Warn().
				Str("evt.name", "infra.cors.origin.invalid").
				Str("origin", entry).
				Msg("ignoring an invalid CORS credentialed origin: it must be an origin like https://example.com")
			continue
		}

		if strings.HasPrefix(host, "*.") {
			domain := strings.TrimPrefix(host, "*.")
			if domain == "" || strings.Contains(domain, "*") {
				log.Warn().
					Str("evt.name", "infra.cors.origin.invalid").
					Str("origin", entry).
					Msg("ignoring an invalid CORS credentialed origin: a wildcard must look like https://*.example.com")
				continue
			}
			l.subdomains = append(l.subdomains, subdomainOrigin{prefix: scheme + "://", suffix: "." + domain})
		} else {
			if strings.Contains(host, "*") {
				log.Warn().
					Str("evt.name", "infra.cors.origin.invalid").
					Str("origin", entry).
					Msg("ignoring an invalid CORS credentialed origin: a wildcard is only allowed as the first label")
				continue
			}
			l.exact[o] = struct{}{}
		}
		accepted = append(accepted, o)
	}

	log.Info().
		Str("evt.name", "infra.cors.origins").
		Strs("origins", accepted).
		Msg("CORS: these origins may send credentialed requests; every other origin gets Access-Control-Allow-Origin: *")

	return l
}

// allows reports whether origin, as the browser sent it, is allowed to make credentialed requests. Browsers serialize
// origins in lowercase, so the comparison is case-sensitive.
func (l originAllowlist) allows(origin string) bool {
	if origin == "" {
		return false
	}
	if _, ok := l.exact[origin]; ok {
		return true
	}
	for _, s := range l.subdomains {
		if label, ok := cutAffixes(origin, s.prefix, s.suffix); ok && isDNSLabel(label) {
			return true
		}
	}
	return false
}

func cutAffixes(s, prefix, suffix string) (string, bool) {
	if len(s) < len(prefix)+len(suffix) || !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, suffix) {
		return "", false
	}
	return s[len(prefix) : len(s)-len(suffix)], true
}

// isDNSLabel reports whether s is a single, non-empty lowercase hostname label.
func isDNSLabel(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}
