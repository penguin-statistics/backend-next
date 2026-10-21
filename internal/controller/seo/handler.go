// Package seo serves the stats pages of the frontend (/{lang}/result/{server}/item[/{itemId}] and
// /{lang}/result/{server}/stage[/{zoneId}/{stageId}]) as the frontend's index.html with the page's metadata,
// structured data and a plain HTML table of its data spliced in, for search engines and link previews. The app
// replaces the table once it starts; everyone gets the same HTML.
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

// shortMaxAge is the max-age of pages without data: 404s, and pages rendered before the matrix was calculated
const shortMaxAge = time.Minute

// isolationHeaders are the headers of the global helmet middleware that isolate a document from other origins: the
// app loads images, scripts and fonts from other origins (CDN, S3, Crisp), which require-corp would block. The
// frontend's own server sends none of them.
var isolationHeaders = []string{
	"Cross-Origin-Embedder-Policy",
	"Cross-Origin-Opener-Policy",
	"Cross-Origin-Resource-Policy",
	"Origin-Agent-Cluster",
}

// Register registers the stats pages if they are enabled, and keeps the frontend's index.html they are made of.
func Register(lc fx.Lifecycle, app *fiber.App, conf *appconfig.Config, data *service.SEOData) {
	if !conf.SEOPagesEnabled {
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
	log.Info().
		Str("evt.name", "seo.pages.enabled").
		Str("shellURL", conf.SEOShellURL).
		Str("siteURL", conf.SEOSiteURL).
		Msg("serving the stats pages with server-rendered metadata and data")
}

type Handler struct {
	site   string
	maxAge time.Duration
	shells ShellSource
	data   Data
	// lists holds the list pages rendered last, by listKey
	lists sync.Map
}

func NewHandler(siteURL string, maxAge time.Duration, shells ShellSource, data Data) *Handler {
	return &Handler{site: strings.TrimRight(siteURL, "/"), maxAge: maxAge, shells: shells, data: data}
}

// Mount registers the routes of the pages: /{lang}/result and everything below it for each language of the stats
// routes of the frontend.
func (h *Handler) Mount(r fiber.Router) {
	for _, lang := range langs {
		r.Get("/"+lang+"/result", h.Serve)
		r.Get("/"+lang+"/result/*", h.Serve)
	}
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

// canonicalPath is the canonical path of the page of r, escaped
func (r route) canonicalPath() string {
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
	for _, k := range isolationHeaders {
		c.Response().Header.Del(k)
	}
	// as requested, not unescaped (fiber's UnescapePath is off)
	path := c.Path()
	r := parseRoute(path)

	shell, err := h.shells.Get(c.UserContext())
	if err != nil {
		h.unavailable(c, r, path)
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
		h.sendRaw(c, shell, r, "shell_only", false)
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
				h.send(c, shell, e.page, e.html)
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
		h.lists.Store(key, &listEntry{catalog: catalog, matrix: matrix, shell: shell, page: p, html: html})
	}
	h.send(c, shell, p, html)
	return nil
}

// listKey and listEntry hold a list page rendered from a catalog, matrix and shell
type listKey struct {
	lang, server string
	kind         pageKind
}

type listEntry struct {
	catalog *service.SEOCatalog
	matrix  *service.SEOMatrix
	shell   *spashell.Shell
	page    *Page
	html    []byte
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
	b := &builder{site: h.site, t: textsByLang[r.lang], r: r, catalog: catalog, matrix: matrix}
	switch r.kind {
	case kindItem:
		p = b.itemPage()
	case kindItemList:
		p = b.itemListPage()
	case kindStage:
		p = b.stagePage()
	case kindStageList:
		p = b.stageListPage()
	default:
		p = b.notFound()
	}
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

func count(kind pageKind, outcome string) {
	observability.SEOPageRenderTotal.WithLabelValues(string(kind), outcome).Inc()
}

func (h *Handler) cacheFor(c *fiber.Ctx, d time.Duration) {
	c.Set(fiber.HeaderCacheControl, "public, max-age="+strconv.Itoa(int(d.Seconds())))
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

func (h *Handler) send(c *fiber.Ctx, shell *spashell.Shell, p *Page, html []byte) {
	count(p.Kind, p.Outcome)
	setHTML(c, p.Head.Lang)
	if p.Status == fiber.StatusOK && p.Cacheable {
		h.cacheFor(c, h.maxAge)
	} else {
		h.cacheFor(c, h.shortMaxAge())
	}
	if !p.Indexable {
		c.Set("X-Robots-Tag", strings.ReplaceAll(p.Head.Robots, ",", ", "))
	}
	lastModified := p.LastModified
	if shell.LastModified.After(lastModified) {
		lastModified = shell.LastModified
	}
	if !lastModified.IsZero() {
		c.Response().Header.SetLastModified(lastModified)
	}
	c.Status(p.Status)
	_ = c.Send(html)
}

// unavailable answers a request while there is no index.html to serve. The ingress routes the stats paths here
// case-sensitively, so the same path with the language in upper case (/ZH/result/...) is served by the frontend,
// whose router matches it case-insensitively and replaces it with the lower case one without reloading: the app
// works, with the language, server and page asked for. A request with the language in upper case was not routed
// so; it gets a 503 instead of a redirect loop.
func (h *Handler) unavailable(c *fiber.Ctx, r route, path string) {
	c.Set(fiber.HeaderCacheControl, "no-store")
	segs := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)
	if r.lang != "" && segs[0] == r.lang {
		count(r.kind, "unavailable_redirect")
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
