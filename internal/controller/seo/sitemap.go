package seo

import (
	"bytes"
	"encoding/xml"
	"strconv"
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/pkg/cachectrl"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/service"
)

// The sitemaps of the pages: /sitemap.xml is an index of one sitemap per language and server,
// /sitemap-{lang}-{server}.xml (16 of them), each listing the pages served there as indexable 200s: the two lists
// and the items and stages they link. With about 1,700 pages per language and server at most (CN, October 2026), a
// sitemap stays far below the limits of 50,000 URLs and 50 MB. The hreflang alternates are in the pages, not
// repeated here, and there is no changefreq or priority, which search engines ignore.
//
// They are all at the root: a sitemap may only list the URLs under the directory it is in (sitemaps.org; Google
// waives this only for one submitted in Search Console), so one at /sitemaps/ could not list /en/result/... The
// ingress routes each of them here by its exact path, as a prefix /sitemap would not match /sitemap-en-cn.xml.
const (
	sitemapIndexPath = "/sitemap.xml"
	sitemapPrefix    = "/sitemap-"

	sitemapNS = "http://www.sitemaps.org/schemas/sitemap/0.9"
	mimeXML   = "application/xml; charset=utf-8"
)

// the "pages" the sitemaps are counted as
const (
	kindSitemapIndex pageKind = "sitemap_index"
	kindSitemap      pageKind = "sitemap"
)

// sitemapPath is the path of the sitemap of the pages in lang on server (upper case)
func sitemapPath(lang, server string) string {
	return sitemapPrefix + lang + "-" + strings.ToLower(server) + ".xml"
}

// sitemapKey is a sitemap of the pages in a language on a server (upper case)
type sitemapKey struct {
	lang, server string
}

// sitemapsByPath are the sitemaps by path
var sitemapsByPath = func() map[string]sitemapKey {
	m := map[string]sitemapKey{}
	for _, lang := range langs {
		for _, server := range constant.Servers {
			m[sitemapPath(lang, server)] = sitemapKey{lang: lang, server: server}
		}
	}
	return m
}()

type xmlURLSet struct {
	XMLName xml.Name        `xml:"urlset"`
	NS      string          `xml:"xmlns,attr"`
	URLs    []xmlSitemapURL `xml:"url"`
}

type xmlSitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

type xmlSitemapIndex struct {
	XMLName  xml.Name        `xml:"sitemapindex"`
	NS       string          `xml:"xmlns,attr"`
	Sitemaps []xmlSitemapURL `xml:"sitemap"`
}

// w3cTime formats t as the W3C datetime of a lastmod, in UTC; "" for the zero time, which a lastmod is left out for
func w3cTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func marshalXML(v any) []byte {
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	// the values are strings: encoding them cannot fail
	_ = enc.Encode(v)
	buf.WriteByte('\n')
	return buf.Bytes()
}

// sitemapEntry holds a sitemap built from a catalog and matrix
type sitemapEntry struct {
	catalog *service.SEOCatalog
	matrix  *service.SEOMatrix
	xml     []byte
	// lastModified is when the sitemap last changed: the later of the times given the matrix and the catalog (see
	// stamps); zero if the matrix has no last modified time
	lastModified time.Time
}

// routable tells whether path, the canonical path of a page, is served as that page: an ID "" would be parsed as
// another path and redirected, and "." or ".." resolved to another path before routing
func routable(path string, kind pageKind) bool {
	if p := path + "/"; strings.Contains(p, "/./") || strings.Contains(p, "/../") {
		return false
	}
	r := parseRoute(path)
	return r.kind == kind && r.canonicalPath() == path
}

// buildSitemap lists the pages in lang on server that are served as indexable 200s: the two lists, and the items
// and stages they link, which are those with rows in the matrix (see builder.listedItems and listedStages). The
// pages without rows are noindex, the recruitment statistics too, and the others are not found.
//
// The lastmod of every page is the time of the matrix, which the page itself gives as its Last-Modified, the
// dateModified of its structured data and the date it says it was updated: search engines use lastmod only while it
// agrees with the pages. That of a closed event does not change in substance afterwards, but would then disagree.
func (h *Handler) buildSitemap(k sitemapKey, catalog *service.SEOCatalog, matrix *service.SEOMatrix) *sitemapEntry {
	b := &builder{site: h.site, t: textsByLang[k.lang], r: route{lang: k.lang, server: k.server}, catalog: catalog, matrix: matrix}
	e := &sitemapEntry{catalog: catalog, matrix: matrix}
	if !matrix.LastModified.IsZero() {
		// the catalog changes the sitemap too, when a stage is added or moved to another zone
		e.lastModified = h.stamps.at(matrixSlot{server: k.server}, matrix, matrix.LastModified)
		if at := h.stamps.at(catalogSlot{}, catalog, catalog.ChangedAt); at.After(e.lastModified) {
			e.lastModified = at
		}
	}
	lastmod := w3cTime(matrix.LastModified)
	set := xmlURLSet{NS: sitemapNS}
	add := func(kind pageKind, ids ...string) {
		path := b.path(k.lang, kind, ids...)
		if !routable(path, kind) {
			return
		}
		set.URLs = append(set.URLs, xmlSitemapURL{Loc: b.abs(path), LastMod: lastmod})
	}
	add(kindItemList)
	add(kindStageList)
	for _, item := range b.listedItems() {
		add(kindItem, item.ArkID)
	}
	for _, stage := range b.listedStages() {
		add(kindStage, stage.Zone.ArkID, stage.ArkID)
	}
	e.xml = marshalXML(set)
	return e
}

// sitemap returns the sitemap of k, built once per catalog and matrix
func (h *Handler) sitemap(k sitemapKey, catalog *service.SEOCatalog, matrix *service.SEOMatrix) *sitemapEntry {
	if v, ok := h.sitemaps.Load(k); ok {
		if e := v.(*sitemapEntry); e.catalog == catalog && e.matrix == matrix {
			return e
		}
	}
	e := h.buildSitemap(k, catalog, matrix)
	h.sitemaps.Store(k, e)
	return e
}

// SitemapIndex serves /sitemap.xml, the index of the sitemaps of each language and server. It is served while some
// matrices are not calculated, listing their sitemaps (503s until then) without a lastmod, so that the index is
// the same list whatever is cold; it is then cached for a minute and has no Last-Modified. While the catalog is not
// loaded it is a 503, like the sitemaps.
func (h *Handler) SitemapIndex(c *fiber.Ctx) error {
	for _, k := range headersNotSentByFrontend {
		c.Response().Header.Del(k)
	}
	// fiber routes /SITEMAP.XML and /sitemap.xml/ here too, which the ingress does not
	if c.Path() != sitemapIndexPath {
		h.sitemapNotFound(c, kindSitemapIndex)
		return nil
	}
	catalog := h.data.Catalog()
	if catalog == nil {
		h.sitemapCold(c, kindSitemapIndex)
		return nil
	}
	index := xmlSitemapIndex{NS: sitemapNS}
	var lastModified time.Time
	complete := true
	for _, lang := range langs {
		for _, server := range constant.Servers {
			entry := xmlSitemapURL{Loc: h.site + sitemapPath(lang, server)}
			if matrix := h.data.Matrix(server); matrix != nil {
				e := h.sitemap(sitemapKey{lang: lang, server: server}, catalog, matrix)
				entry.LastMod = w3cTime(e.lastModified)
				if e.lastModified.After(lastModified) {
					lastModified = e.lastModified
				}
			} else {
				complete = false
			}
			index.Sitemaps = append(index.Sitemaps, entry)
		}
	}
	if !complete {
		lastModified = time.Time{}
	}
	h.sendSitemap(c, kindSitemapIndex, marshalXML(index), lastModified)
	return nil
}

// Sitemap serves /sitemap-{lang}-{server}.xml, the sitemap of the pages in a language on a server.
func (h *Handler) Sitemap(c *fiber.Ctx) error {
	for _, k := range headersNotSentByFrontend {
		c.Response().Header.Del(k)
	}
	// as requested: fiber routes the paths case-insensitively
	k, ok := sitemapsByPath[c.Path()]
	if !ok {
		h.sitemapNotFound(c, kindSitemap)
		return nil
	}
	catalog := h.data.Catalog()
	if catalog == nil {
		h.sitemapCold(c, kindSitemap)
		return nil
	}
	matrix := h.data.Matrix(k.server)
	if matrix == nil {
		h.sitemapCold(c, kindSitemap)
		return nil
	}
	e := h.sitemap(k, catalog, matrix)
	h.sendSitemap(c, kindSitemap, e.xml, e.lastModified)
	return nil
}

// sendSitemap sends a sitemap last modified at lastModified, cached like the pages with data (zero: for a minute,
// without Last-Modified), or 304 Not Modified to a conditional request not older than it (none while lastModified is
// later than the Date, see setLastModified). It is noindex: the backend serves it on its own hosts too, and a sitemap
// is no page to find in search results.
func (h *Handler) sendSitemap(c *fiber.Ctx, kind pageKind, body []byte, lastModified time.Time) {
	c.Set(fiber.HeaderContentType, mimeXML)
	c.Set("X-Robots-Tag", "noindex")
	if lastModified.IsZero() {
		h.cacheFor(c, h.shortMaxAge())
	} else {
		h.cachePage(c)
		if setLastModified(c, lastModified) && cachectrl.NotModified(c) {
			count(kind, "not_modified")
			c.Status(fiber.StatusNotModified)
			return
		}
	}
	count(kind, "ok")
	c.Status(fiber.StatusOK)
	_ = c.Send(body)
}

// sitemapCold answers while the catalog is not loaded or the matrix of the server not calculated: a 503 that
// search engines retry later, as the pages are
func (h *Handler) sitemapCold(c *fiber.Ctx, kind pageKind) {
	count(kind, "cold")
	middlewares.ExpectStatus(c)
	c.Set(fiber.HeaderContentType, fiber.MIMETextPlainCharsetUTF8)
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set(fiber.HeaderRetryAfter, strconv.Itoa(int(retryAfterCold.Seconds())))
	c.Status(fiber.StatusServiceUnavailable)
	_ = c.SendString("The drop data is being calculated. Please retry later.\n")
}

// sitemapNotFound answers a path that is no sitemap
func (h *Handler) sitemapNotFound(c *fiber.Ctx, kind pageKind) {
	count(kind, "not_found")
	middlewares.ExpectStatus(c)
	h.cacheFor(c, h.shortMaxAge())
	sendSitemapNotFound(c)
}

func sendSitemapNotFound(c *fiber.Ctx) {
	c.Set(fiber.HeaderContentType, fiber.MIMETextPlainCharsetUTF8)
	c.Status(fiber.StatusNotFound)
	_ = c.SendString("Not Found\n")
}

// mountSitemaps registers the routes of the sitemaps
func mountSitemaps(r fiber.Router, index, sitemap fiber.Handler) {
	r.Get(sitemapIndexPath, index)
	r.Get(sitemapPrefix+"*", sitemap)
}

// sitemapsDisabled answers the sitemaps while the pages are off: not found, never the frontend's index.html as a
// 200, so that an ingress routing them here before the flag is on serves no soft 404
func sitemapsDisabled(kind pageKind) fiber.Handler {
	return func(c *fiber.Ctx) error {
		for _, k := range headersNotSentByFrontend {
			c.Response().Header.Del(k)
		}
		count(kind, "disabled")
		middlewares.ExpectStatus(c)
		c.Set(fiber.HeaderCacheControl, "no-store")
		sendSitemapNotFound(c)
		return nil
	}
}
