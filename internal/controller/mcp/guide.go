package mcp

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/text/language"

	"exusiai.dev/backend-next/internal/controller/mcp/guide"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
)

// The setup guide (package guide) is served at the root of the host of the public URL, mcp.penguin-stats.io, so that
// a person or an agent who opens the host finds out how to add the server to an app; until then the root was a bare
// 404. It is negotiated: browsers and link previews get the HTML, agents asking for Markdown get the Markdown, in the
// language of ?lang or Accept-Language. /index.md?lang= serves the Markdown of a language whatever the request,
// /llms.txt the English Markdown for agents, /robots.txt keeps crawlers off the endpoint, and /fonts/ the fonts of the
// page, whose names change with them, and their license. The guide is served to its host only: the backend answers
// other hosts too (penguin-stats.cn, through its gateway), whose root is something else.
//
// An MCP client given the URL of the guide instead of the endpoint is helped on: its POST is redirected to the
// endpoint with a 307, which keeps the method and the body (fetch, Python's requests and httpx, and Go follow it),
// and a GET for an event stream gets a 406 naming the endpoint. Both carry a JSON-RPC error that names it, for the
// clients that do not follow redirects to show.

const (
	mimeMarkdown = "text/markdown; charset=utf-8"
	// the root varies by Accept and Accept-Language, which Cloudflare ignores: private keeps the variants out of shared
	// caches, should a rule ever cache everything
	guideRootCacheControl = "private, max-age=300"
	// /index.md?lang=, /llms.txt and /robots.txt are the same for every request to their URL, whatever its Origin (CORS
	// answers them in its public tier, see middlewares.MCPGuidePaths): Cloudflare caches robots.txt by default, and
	// the others should a rule cache them
	guideFileCacheControl = "public, max-age=3600"
	robotsCacheControl    = "public, max-age=86400"
	// a font's path changes with the font (guide.File.Immutable): browsers and Cloudflare, which caches fonts by their
	// extension, keep it for good
	immutableCacheControl = "public, max-age=31536000, immutable"
)

// guideHandler serves the guide.
type guideHandler struct {
	// host is the host of the guide, that of the public URL
	host string
	// endpoint is the public URL, notTheEndpoint the error telling a client it is not
	endpoint, notTheEndpoint string
	// pages are the responses of the pages by language (see guideLang)
	pages        map[string]*guidePage
	llms, robots *static
}

// guidePage holds the responses of a page in a language: at the root, its HTML, its Markdown and the same as text
// (see negotiateGuide), and at /index.md its Markdown again, which is cached publicly.
type guidePage struct {
	html, markdown, plain, index *static
}

// mountGuide serves the guide of the server whose public URL is endpoint on the host of endpoint, at /, /index.md,
// /llms.txt, /robots.txt and under /fonts/. site is the website (SEOSiteURL); limit requests per window is the rate
// limit of /mcp.
func mountGuide(r fiber.Router, endpoint *url.URL, site string, limit int, window time.Duration) error {
	root := guideURL(endpoint)
	origin := strings.TrimSuffix(root, "/")
	card := endpoint.String() + strings.TrimPrefix(middlewares.MCPServerCardPath, middlewares.MCPPath)
	g, err := guide.Render(guide.Config{
		Endpoint:   endpoint.String(),
		Guide:      root,
		Card:       card,
		Site:       site,
		Repository: repositoryURL,
		RateLimit:  limit,
		RateWindow: window,
	})
	if err != nil {
		return err
	}

	license := `<https://creativecommons.org/licenses/by-nc/4.0/>; rel="license"`
	serviceDesc := "<" + card + `>; rel="service-desc"; type="` + mimeServerCard + `"`
	llms := "<" + origin + guide.LLMsPath + `>; rel="describedby"; type="text/plain"`
	h := &guideHandler{
		host:           endpoint.Hostname(),
		endpoint:       endpoint.String(),
		notTheEndpoint: "This is the setup guide. The MCP endpoint is " + endpoint.String(),
		pages:          make(map[string]*guidePage, len(g.Pages)),
		llms: guideHeaders(newStatic(fiber.MIMETextPlainCharsetUTF8, guideFileCacheControl, g.LLMs), "en").
			with(fiber.HeaderLink, license+", "+serviceDesc),
		robots: guideHeaders(newStatic(fiber.MIMETextPlainCharsetUTF8, robotsCacheControl, g.Robots), ""),
	}
	for lang, p := range g.Pages {
		html := root + "?lang=" + lang
		markdown := origin + guide.MarkdownPath + "?lang=" + lang
		h.pages[lang] = &guidePage{
			html: guideHeaders(newStatic(fiber.MIMETextHTMLCharsetUTF8, guideRootCacheControl, p.HTML), p.ContentLanguage).
				varies(fiber.HeaderAccept, fiber.HeaderAcceptLanguage).
				with(fiber.HeaderLink, "<"+markdown+`>; rel="alternate"; type="text/markdown", `+llms+", "+license+", "+serviceDesc).
				with(fiber.HeaderContentSecurityPolicy, g.CSP).
				// instead of helmet's require-corp, which blocks the icon: penguin-stats.io serves it without
				// Cross-Origin-Resource-Policy, and the page needs no cross-origin isolation
				with("Cross-Origin-Embedder-Policy", "unsafe-none"),
			markdown: guideHeaders(newStatic(mimeMarkdown, guideRootCacheControl, p.Markdown), p.ContentLanguage).
				varies(fiber.HeaderAccept, fiber.HeaderAcceptLanguage).
				with(fiber.HeaderLink, "<"+html+`>; rel="alternate"; type="text/html", `+llms+", "+license+", "+serviceDesc),
			plain: guideHeaders(newStatic(fiber.MIMETextPlainCharsetUTF8, guideRootCacheControl, p.Markdown), p.ContentLanguage).
				varies(fiber.HeaderAccept, fiber.HeaderAcceptLanguage).
				with(fiber.HeaderLink, "<"+html+`>; rel="alternate"; type="text/html", `+llms+", "+license+", "+serviceDesc),
			// the page is the one to find in search results
			index: guideHeaders(newStatic(mimeMarkdown, guideFileCacheControl, p.Markdown), p.ContentLanguage).
				with(fiber.HeaderLink, "<"+html+`>; rel="canonical", `+license+", "+serviceDesc),
		}
	}

	r.Get("/", h.onHost, h.root)
	r.Post("/", h.onHost, h.post)
	r.Get(guide.MarkdownPath, h.onHost, h.index)
	r.Get(guide.LLMsPath, h.onHost, h.llms.send)
	r.Get(guide.RobotsPath, h.onHost, h.robots.send)
	for _, f := range g.Files {
		cacheControl := robotsCacheControl
		if f.Immutable {
			cacheControl = immutableCacheControl
		}
		r.Get(f.Path, h.onHost, guideHeaders(newStatic(f.ContentType, cacheControl, f.Body), "").send)
	}
	// any other file there, such as a font of another version of the page while a release rolls out, is not found for
	// now only: Cloudflare would otherwise keep the 404 of a font for minutes, for every visitor
	r.Get(guide.FontsPath+"*", h.onHost, func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "no-store")
		return fiber.ErrNotFound
	})
	return nil
}

// guideHeaders adds the headers of every response of the guide to s, and its language if any: no sniffing of its
// type, and no Referer to the sites it links to (the documentation of the apps), as the page shares nothing.
func guideHeaders(s *static, contentLanguage string) *static {
	s.with(fiber.HeaderXContentTypeOptions, "nosniff").with(fiber.HeaderReferrerPolicy, "no-referrer")
	if contentLanguage != "" {
		s.with(fiber.HeaderContentLanguage, contentLanguage)
	}
	return s
}

// onHost passes the requests to the host of the guide on, and answers 404 to those to other hosts, as before.
func (h *guideHandler) onHost(c *fiber.Ctx) error {
	host := c.Hostname()
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	if !strings.EqualFold(host, h.host) {
		return fiber.ErrNotFound
	}
	return c.Next()
}

// root answers GET and HEAD at the root: the page in the format and the language of the request.
func (h *guideHandler) root(c *fiber.Ctx) error {
	format := negotiateGuide(c.Get(fiber.HeaderAccept))
	if format == guideNotAcceptable {
		// an MCP client opening an event stream (the old HTTP+SSE transport, or the GET of Streamable HTTP)
		c.Vary(fiber.HeaderAccept)
		c.Set(fiber.HeaderCacheControl, "no-store")
		middlewares.ExpectStatus(c)
		return sendError(c, fiber.StatusNotAcceptable, codeServerError, h.notTheEndpoint)
	}
	p := h.pages[guideLang(c.Query("lang"), c.Get(fiber.HeaderAcceptLanguage))]
	switch format {
	case guideMarkdown:
		return p.markdown.send(c)
	case guidePlain:
		return p.plain.send(c)
	default:
		return p.html.send(c)
	}
}

// post sends a POST to the root, a client given the URL of the guide, on to the endpoint.
func (h *guideHandler) post(c *fiber.Ctx) error {
	c.Set(fiber.HeaderLocation, h.endpoint)
	c.Set(fiber.HeaderCacheControl, "no-store")
	middlewares.ExpectStatus(c)
	return sendErrorTo(c, requestID(c.Body()), fiber.StatusTemporaryRedirect, codeServerError, h.notTheEndpoint)
}

// index serves the Markdown of the language of ?lang, English without one: the URL names the language.
func (h *guideHandler) index(c *fiber.Ctx) error {
	lang, ok := guideLangOf(c.Query("lang"))
	if !ok {
		lang = guide.DefaultLang
	}
	return h.pages[lang].index.send(c)
}

// guideFormat is what the root answers a request with
type guideFormat int

const (
	guideHTML guideFormat = iota
	guideMarkdown
	// guidePlain is the Markdown as text/plain, for a client that takes text only
	guidePlain
	// guideNotAcceptable answers an MCP client asking for an event stream
	guideNotAcceptable
)

// negotiateGuide is the format of the root for the Accept header accept. Markdown when it is preferred to HTML,
// at least as much; HTML when it is asked for; the Markdown as text/plain when only text/plain is; a 406 when an
// event stream is asked for without HTML; HTML otherwise, */* and none included: the link previews of QQ, WeChat and
// Discord need its og: tags, and the tools turning HTML into text read it well.
func negotiateGuide(accept string) guideFormat {
	q := acceptQ(accept)
	md := max(q["text/markdown"], q["text/x-markdown"])
	html := max(q["text/html"], q["application/xhtml+xml"])
	switch {
	case q["text/event-stream"] > 0 && html == 0:
		return guideNotAcceptable
	case md > 0 && md >= html:
		return guideMarkdown
	case html > 0:
		return guideHTML
	case q["text/plain"] > 0:
		return guidePlain
	default:
		return guideHTML
	}
}

// acceptQ returns the quality the Accept header accept gives each media type it lists, in lower case: the highest
// it gives, 0 for a q that is not a number from 0 to 1. Wildcards are media types like the others.
func acceptQ(accept string) map[string]float64 {
	qs := make(map[string]float64)
	for _, mediaRange := range strings.Split(accept, ",") {
		mediaType, params, _ := strings.Cut(mediaRange, ";")
		q := 1.0
		for _, param := range strings.Split(params, ";") {
			if name, value, ok := strings.Cut(param, "="); ok && strings.EqualFold(strings.TrimSpace(name), "q") {
				if v, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && v >= 0 && v <= 1 {
					q = v
				} else {
					q = 0
				}
			}
		}
		if t := strings.ToLower(strings.TrimSpace(mediaType)); t != "" {
			qs[t] = max(qs[t], q)
		}
	}
	return qs
}

// guideLang is the language of the guide for a request: that of ?lang (lang) if the guide has it, else the first of
// Accept-Language (acceptLanguage) it has, else English.
func guideLang(lang, acceptLanguage string) string {
	if l, ok := guideLangOf(lang); ok {
		return l
	}
	// sorted by q, without those of q=0; nil if malformed
	tags, _, _ := language.ParseAcceptLanguage(acceptLanguage)
	for _, tag := range tags {
		base, _ := tag.Base()
		if l, ok := guideLangOf(base.String()); ok {
			return l
		}
	}
	return guide.DefaultLang
}

// guideLangOf is the language of the guide for the language tag tag: Chinese for zh, whatever its script or region
// (there is no Traditional Chinese yet), English for en, and for ja and ko until they are translated: their readers
// get English rather than a page they may not read at all.
func guideLangOf(tag string) (string, bool) {
	primary, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
	switch primary {
	case "zh":
		return "zh", true
	case "en", "ja", "ko":
		return "en", true
	}
	return "", false
}
