// Package seo serves the stats pages of the frontend (/{lang}/result/{server}/item[/{itemId}] and
// /{lang}/result/{server}/stage[/{zoneId}/{stageId}]) as the frontend's index.html with the page's metadata,
// structured data and a plain HTML table of its data spliced in, for search engines and link previews. The app
// replaces the table once it starts; everyone gets the same HTML. The pages have a Markdown view too, at their path
// with .md appended, for agents (see markdown.go).
package seo

import (
	"bytes"
	"context"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/pkg/cachectrl"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/pkg/spashell"
	"exusiai.dev/backend-next/internal/service"
)

// Data is the data the pages are made of.
type Data interface {
	// Catalog returns the items, stages and zones, or nil before they are loaded
	Catalog() *service.SEOCatalog
	// Matrix returns the matrix of a server, or nil while it is not calculated
	Matrix(server string) *service.SEOMatrix
}

// ShellSource gives the frontend's index.html.
type ShellSource interface {
	Get(ctx context.Context) (*spashell.Shell, error)
}

// shortMaxAge is the max-age of pages without data: 404s, and the lists rendered before the matrix was calculated
const shortMaxAge = time.Minute

// retryAfterCold is the Retry-After of the 503s served while the data is loaded or calculated after a start: the
// catalog takes seconds, the matrices up to a few minutes
const retryAfterCold = 120 * time.Second

// headersNotSentByFrontend are the headers of the global middlewares that the frontend's own server does not send
// for these pages, removed so the pages behave as the app does elsewhere on the site:
//   - helmet's isolation of a document from other origins: the app loads images, scripts and fonts from other
//     origins (CDN, S3, Crisp), which require-corp would block;
//   - helmet's X-Frame-Options, X-DNS-Prefetch-Control (the app preconnects to its CDN), X-Download-Options and
//     X-Permitted-Cross-Domain-Policies;
//   - the CORS headers, which vary by Origin (Access-Control-Allow-Credentials and the Origin echoed for
//     allow-listed origins) on responses that are otherwise the same for everyone and cached publicly.
var headersNotSentByFrontend = []string{
	"Cross-Origin-Embedder-Policy",
	"Cross-Origin-Opener-Policy",
	"Cross-Origin-Resource-Policy",
	"Origin-Agent-Cluster",
	fiber.HeaderXFrameOptions,
	fiber.HeaderXDNSPrefetchControl,
	fiber.HeaderXDownloadOptions,
	fiber.HeaderXPermittedCrossDomainPolicies,
	fiber.HeaderAccessControlAllowOrigin,
	fiber.HeaderAccessControlAllowCredentials,
	fiber.HeaderAccessControlExposeHeaders,
	fiber.HeaderVary,
}

// browserMaxAge caps the max-age of the pages with data in browsers: a page refers to the scripts of the frontend
// release whose index.html it was made of, which a release removes. Shared caches keep them for the max-age of the
// pages (s-maxage).
const browserMaxAge = time.Minute

// Register registers the stats pages if they are enabled, and keeps the frontend's index.html they are made of.
// Disabled, or with a shell URL that cannot be fetched, the stats paths are still registered, to send their requests
// back to the frontend (see unavailable): the ingress may route them here while the pages are off, when it was
// switched on before this flag, or the flag was switched off or the backend rolled back without switching it off.
func Register(lc fx.Lifecycle, app *fiber.App, conf *appconfig.Config, data *service.SEOData) {
	if !conf.SEOPagesEnabled {
		MountDisabled(app)
		return
	}
	if err := spashell.ValidateURL(conf.SEOShellURL); err != nil {
		log.Error().
			Str("evt.name", "seo.pages.invalid_shell_url").
			Err(err).
			Msg("PENGUIN_V3_SEO_SHELL_URL is not an absolute http(s) URL: leaving the stats pages to the frontend")
		MountDisabled(app)
		return
	}
	src := spashell.New(spashell.Config{URL: conf.SEOShellURL, TTL: conf.SEOShellTTL, Timeout: conf.SEOShellFetchTimeout})
	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go src.Run(ctx)
			return nil
		},
		OnStop: func(context.Context) error {
			cancel()
			return nil
		},
	})
	NewHandler(conf.SEOSiteURL, conf.SEOPageMaxAge, src, data).Mount(app)
	effective := src.Config()
	evt := log.Info()
	if effective.TTL != conf.SEOShellTTL || effective.Timeout != conf.SEOShellFetchTimeout {
		evt = log.Warn()
	}
	evt.
		Str("evt.name", "seo.pages.enabled").
		Str("shellURL", conf.SEOShellURL).
		Dur("shellTTL", effective.TTL).
		Dur("shellFetchTimeout", effective.Timeout).
		Str("siteURL", conf.SEOSiteURL).
		Msg("serving the stats pages with server-rendered metadata and data (a shell TTL or fetch timeout that is " +
			"not positive is replaced by its default)")
}

// MountDisabled registers the routes of the pages to send their requests back to the frontend, and those of their
// sitemaps to answer 404, while the pages are off.
func MountDisabled(r fiber.Router) {
	disabled := func(c *fiber.Ctx) error {
		path := c.Path()
		r := parseRoute(path)
		if r.md {
			markdownDisabled(c, r)
			return nil
		}
		(&Handler{}).unavailable(c, r, path, "disabled_redirect")
		return nil
	}
	for _, lang := range service.StatsLangs {
		r.Get("/"+lang+"/result", disabled)
		r.Get("/"+lang+"/result/*", disabled)
	}
	mountSitemaps(r, sitemapsDisabled(kindSitemapIndex), sitemapsDisabled(kindSitemap))
}

type Handler struct {
	site   string
	maxAge time.Duration
	shells ShellSource
	data   Data
	// lists holds the list pages rendered last, by listKey
	lists sync.Map
	// sitemaps holds the sitemaps built last, by sitemapKey
	sitemaps sync.Map
	// stamps holds the times given the data the sitemaps and pages are made of
	stamps stamps
}

func NewHandler(siteURL string, maxAge time.Duration, shells ShellSource, data Data) *Handler {
	return &Handler{site: strings.TrimRight(siteURL, "/"), maxAge: maxAge, shells: shells, data: data}
}

// Mount registers the routes of the pages: /{lang}/result and everything below it for each language of the stats
// routes of the frontend, and their sitemaps.
func (h *Handler) Mount(r fiber.Router) {
	for _, lang := range service.StatsLangs {
		r.Get("/"+lang+"/result", h.Serve)
		r.Get("/"+lang+"/result/*", h.Serve)
	}
	mountSitemaps(r, h.SitemapIndex, h.Sitemap)
}

// parseRoute parses a path of the stats routes as requested (escaped); it is one of the routes of Mount
func parseRoute(path string) route {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	r := route{kind: kindNone}
	if len(segs) < 2 {
		return r
	}
	for i, seg := range segs {
		unescaped, err := url.PathUnescape(seg)
		if err != nil {
			return r
		}
		segs[i] = unescaped
	}
	r.lang = strings.ToLower(segs[0])
	rest := segs[2:]
	if len(rest) == 0 {
		r.kind = kindOther
		return r
	}
	// the Markdown view of the page of the path without .md: no ID of an item or a stage ends in .md (see routable),
	// and a last segment that is just .md is an ID, not found
	if last := rest[len(rest)-1]; len(last) > len(mdSuffix) && strings.HasSuffix(last, mdSuffix) {
		rest[len(rest)-1] = strings.TrimSuffix(last, mdSuffix)
		r.md = true
	}
	server := strings.ToUpper(rest[0])
	if _, ok := constant.LocMap[server]; !ok {
		return r
	}
	r.server = server
	switch {
	case len(rest) == 1:
		r.kind = kindOther
	case strings.EqualFold(rest[1], "item") && len(rest) == 2:
		r.kind = kindItemList
	case strings.EqualFold(rest[1], "item") && len(rest) == 3:
		r.kind, r.itemID = kindItem, rest[2]
	case strings.EqualFold(rest[1], "stage") && len(rest) == 2:
		r.kind = kindStageList
	case strings.EqualFold(rest[1], "stage") && len(rest) == 4:
		r.kind, r.zoneID, r.stageID = kindStage, rest[2], rest[3]
	}
	return r
}

// canonicalPath is the canonical path of the page of r, or of its Markdown view, escaped
func (r route) canonicalPath() string {
	if r.md {
		return r.pagePath() + mdSuffix
	}
	return r.pagePath()
}

// pagePath is the canonical path of the page of r, escaped
func (r route) pagePath() string {
	var ids []string
	switch r.kind {
	case kindItem:
		ids = []string{r.itemID}
	case kindStage:
		ids = []string{r.zoneID, r.stageID}
	}
	return pagePath(r.lang, r.server, r.kind, ids...)
}

// Serve serves a page. It never returns an error: the error handler of the app answers in JSON.
func (h *Handler) Serve(c *fiber.Ctx) error {
	for _, k := range headersNotSentByFrontend {
		c.Response().Header.Del(k)
	}
	// as requested, not unescaped (fiber's UnescapePath is off)
	path := c.Path()
	r := parseRoute(path)
	if r.md {
		// made of the data alone: served whatever the shell
		h.serveMarkdown(c, r, path)
		return nil
	}

	shell, err := h.shells.Get(c.UserContext())
	if err != nil {
		h.unavailable(c, r, path, "unavailable_redirect")
		return nil
	}

	switch r.kind {
	case kindItem, kindItemList, kindStage, kindStageList:
		if canonical := r.canonicalPath(); path != canonical {
			h.redirect(c, r.kind, canonical)
			return nil
		}
	case kindOther:
		// the frontend redirects these to its home page
		h.sendRaw(c, shell, r, "noindex", true)
		return nil
	}

	catalog := h.data.Catalog()
	if catalog == nil {
		// right after a start: tell search engines to come back instead of indexing the bare app
		h.sendCold(c, shell, r)
		return nil
	}

	var matrix *service.SEOMatrix
	if r.server != "" {
		matrix = h.data.Matrix(r.server)
	}

	// the list pages are the largest, and the same until the data or the shell changes
	var key listKey
	if r.kind == kindItemList || r.kind == kindStageList {
		key = listKey{lang: r.lang, server: r.server, kind: r.kind}
		if e, ok := h.lists.Load(key); ok {
			if e := e.(*listEntry); e.catalog == catalog && e.matrix == matrix && e.shell == shell {
				h.send(c, shell, catalog, matrix, e.page, e.body)
				return nil
			}
		}
	}

	p, html, ok := h.build(r, catalog, matrix, shell)
	if !ok {
		h.sendRaw(c, shell, r, "error", false)
		return nil
	}
	if p.Redirect != "" {
		h.redirect(c, p.Kind, p.Redirect)
		return nil
	}
	if key.kind != "" {
		h.lists.Store(key, &listEntry{catalog: catalog, matrix: matrix, shell: shell, page: p, body: html})
	}
	h.send(c, shell, catalog, matrix, p, html)
	return nil
}

// listKey and listEntry hold a list page rendered from a catalog, matrix and shell, or its Markdown view rendered
// from a catalog and matrix
type listKey struct {
	lang, server string
	kind         pageKind
	md           bool
}

type listEntry struct {
	catalog *service.SEOCatalog
	matrix  *service.SEOMatrix
	// shell is nil for a Markdown view
	shell *spashell.Shell
	page  *Page
	body  []byte
}

// build builds and renders the page of r; ok is false if that failed
func (h *Handler) build(r route, catalog *service.SEOCatalog, matrix *service.SEOMatrix, shell *spashell.Shell) (p *Page, html []byte, ok bool) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Error().
				Str("evt.name", "seo.page.panic").
				Interface("panic", rec).
				Str("lang", r.lang).Str("server", r.server).Str("kind", string(r.kind)).
				Msg("failed to build a stats page; serving the app as is")
			ok = false
		}
	}()
	p = h.builder(r, catalog, matrix).page()
	if p.Redirect != "" {
		return p, nil, true
	}
	head, body, err := renderMarkup(p)
	if err != nil {
		log.Error().
			Str("evt.name", "seo.page.render_failed").
			Err(err).
			Msg("failed to render a stats page; serving the app as is")
		return nil, nil, false
	}
	var buf bytes.Buffer
	shell.Render(&buf, p.Head.Lang, head, body)
	return p, buf.Bytes(), true
}

// builder is the builder of the pages of r
func (h *Handler) builder(r route, catalog *service.SEOCatalog, matrix *service.SEOMatrix) *builder {
	return &builder{site: h.site, t: textsByLang[r.lang], r: r, catalog: catalog, matrix: matrix}
}

func count(kind pageKind, outcome string) {
	observability.SEOPageRenderTotal.WithLabelValues(string(kind), outcome).Inc()
}

func (h *Handler) cacheFor(c *fiber.Ctx, d time.Duration) {
	c.Set(fiber.HeaderCacheControl, "public, max-age="+strconv.Itoa(int(d.Seconds())))
}

// cachePage sets the caching of a page with data: browsers keep it for browserMaxAge at most, shared caches for the
// max-age of the pages
func (h *Handler) cachePage(c *fiber.Ctx) {
	if h.maxAge <= browserMaxAge {
		h.cacheFor(c, h.maxAge)
		return
	}
	c.Set(fiber.HeaderCacheControl, "public, max-age="+strconv.Itoa(int(browserMaxAge.Seconds()))+
		", s-maxage="+strconv.Itoa(int(h.maxAge.Seconds())))
}

func (h *Handler) shortMaxAge() time.Duration {
	if h.maxAge < shortMaxAge {
		return h.maxAge
	}
	return shortMaxAge
}

func withQuery(c *fiber.Ctx, path string) string {
	if q := c.Request().URI().QueryString(); len(q) > 0 {
		return path + "?" + string(q)
	}
	return path
}

// redirect redirects to the canonical path of the page: the path differs in case, a trailing slash, or the zone
func (h *Handler) redirect(c *fiber.Ctx, kind pageKind, path string) {
	count(kind, "redirect")
	h.cacheFor(c, h.maxAge)
	c.Set(fiber.HeaderLocation, withQuery(c, path))
	c.Status(fiber.StatusMovedPermanently)
}

func setHTML(c *fiber.Ctx, lang string) {
	c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
	if lang != "" {
		c.Set(fiber.HeaderContentLanguage, lang)
	}
}

// sendRaw sends the app as is; noindex keeps search engines from indexing it, else it is not cached
func (h *Handler) sendRaw(c *fiber.Ctx, shell *spashell.Shell, r route, outcome string, noindex bool) {
	count(r.kind, outcome)
	setHTML(c, "")
	if noindex {
		c.Set("X-Robots-Tag", "noindex")
		h.cacheFor(c, h.shortMaxAge())
	} else {
		c.Set(fiber.HeaderCacheControl, "no-store")
	}
	c.Status(fiber.StatusOK)
	_ = c.Send(shell.Raw())
}

// sendCold sends the app as is while the catalog is not loaded: a 503 that search engines retry later
func (h *Handler) sendCold(c *fiber.Ctx, shell *spashell.Shell, r route) {
	count(r.kind, "shell_only")
	middlewares.ExpectStatus(c)
	setHTML(c, "")
	c.Set("X-Robots-Tag", "noindex")
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set(fiber.HeaderRetryAfter, strconv.Itoa(int(retryAfterCold.Seconds())))
	c.Status(fiber.StatusServiceUnavailable)
	_ = c.Send(shell.Raw())
}

// send sends a page, or 304 Not Modified without a body to a conditional request for a page made with the matrix
// that is not older than its Last-Modified: the latest of the times given the matrix, the catalog and the shell it
// was made of (see stamps), if not later than the Date (see setLastModified)
func (h *Handler) send(c *fiber.Ctx, shell *spashell.Shell, catalog *service.SEOCatalog, matrix *service.SEOMatrix, p *Page, html []byte) {
	setHTML(c, p.Head.Lang)
	if p.Head.Markdown != "" {
		// for agents: the Markdown view of the page, also linked in its head, and the guide to the site, which the
		// frontend's index.html links in the head already (/llms.txt, frontend-v2 v3.13.42)
		c.Append(fiber.HeaderLink, "<"+p.Head.Markdown+`>; rel="alternate"; type="text/markdown"`, "<"+h.site+`/llms.txt>; rel="describedby"`)
	}
	h.sendPage(c, p.Kind, shell, catalog, matrix, p, html)
}

// sendPage sends p rendered as body, its content type set, as send does; it is counted as kind, and shell is the
// shell it was made of, nil for a Markdown view, which is made of the data alone
func (h *Handler) sendPage(c *fiber.Ctx, kind pageKind, shell *spashell.Shell, catalog *service.SEOCatalog, matrix *service.SEOMatrix, p *Page, body []byte) {
	// a 404 of a page that does not exist (crawlers keep requesting retired IDs) or a 503 while the data is
	// calculated after a start is no error of the backend; their outcomes are counted below
	middlewares.ExpectStatus(c)
	cacheable := p.Status == fiber.StatusOK && p.Cacheable
	switch {
	case p.Status == fiber.StatusServiceUnavailable:
		c.Set(fiber.HeaderCacheControl, "no-store")
		c.Set(fiber.HeaderRetryAfter, strconv.Itoa(int(retryAfterCold.Seconds())))
	case cacheable:
		h.cachePage(c)
	default:
		h.cacheFor(c, h.shortMaxAge())
	}
	if !p.Indexable {
		c.Set("X-Robots-Tag", strings.ReplaceAll(p.Head.Robots, ",", ", "))
	}
	// only a 200 made with the matrix (cacheable), indexable or not, has a Last-Modified: that of a 404, a 503 or a
	// list rendered before the matrix was calculated would be the time of the shell alone, as the first catalog after
	// a start has no time, which the page made with the matrix can have too (the matrix seen first keeps its own
	// time, see stamps), and a client having the list without data be answered that it is not modified
	if cacheable {
		if lastModified := h.lastModified(shell, catalog, matrix); !lastModified.IsZero() && setLastModified(c, lastModified) && cachectrl.NotModified(c) {
			count(kind, "not_modified")
			c.Status(fiber.StatusNotModified)
			return
		}
	}
	count(kind, p.Outcome)
	c.Status(p.Status)
	_ = c.Send(body)
}

// lastModified returns when a page made with shell (nil for none), catalog and matrix last changed: the latest of
// the times given them (see stamps)
func (h *Handler) lastModified(shell *spashell.Shell, catalog *service.SEOCatalog, matrix *service.SEOMatrix) time.Time {
	// the times given the data rather than their own; the catalog's too, for a name changed or a stage moved to
	// another zone
	lastModified := h.stamps.at(matrixSlot{server: matrix.Server}, matrix, matrix.LastModified)
	if at := h.stamps.at(catalogSlot{}, catalog, catalog.ChangedAt); at.After(lastModified) {
		lastModified = at
	}
	// a shell sent anew keeps its time, and a different one has a later one (see spashell.Shell)
	if shell != nil {
		if at := h.stamps.at(shellSlot{}, shell.LastModified.UnixNano(), shell.LastModified); at.After(lastModified) {
			lastModified = at
		}
	}
	return lastModified
}

// unavailable answers a request while there is no index.html to serve, or the pages are off. The ingress routes the stats paths here
// case-sensitively, so the same path with the language in upper case (/ZH/result/...) is served by the frontend,
// whose router matches it case-insensitively and replaces it with the lower case one without reloading: the app
// works, with the language, server and page asked for. A request with the language in upper case was not routed
// so; it gets a 503 instead of a redirect loop.
func (h *Handler) unavailable(c *fiber.Ctx, r route, path, redirectOutcome string) {
	c.Set(fiber.HeaderCacheControl, "no-store")
	segs := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)
	if r.lang != "" && segs[0] == r.lang {
		count(r.kind, redirectOutcome)
		location := "/" + strings.ToUpper(r.lang)
		if len(segs) == 2 {
			// as requested: escaped
			location += "/" + segs[1]
		}
		c.Set(fiber.HeaderLocation, withQuery(c, location))
		c.Status(fiber.StatusFound)
		return
	}
	count(r.kind, "unavailable")
	setHTML(c, "")
	c.Set(fiber.HeaderRetryAfter, "30")
	c.Status(fiber.StatusServiceUnavailable)
	_ = c.SendString(`<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="robots" content="noindex">` +
		`<title>Penguin Statistics</title></head><body><p>Temporarily unavailable. Please reload in a minute.</p>` +
		`<p><a href="/">penguin-stats.io</a></p></body></html>`)
}
