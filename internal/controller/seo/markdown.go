package seo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/service"
)

// The Markdown views of the pages (meta#18), for agents that read Markdown rather than HTML: the path of a page with
// .md appended (/en/result/cn/item/30012.md, /en/result/cn/stage.md) serves the data of the page as Markdown. A view
// starts with YAML front matter: the title of the page, its server and IDs, when the matrix it shows was calculated
// (as_of), the page itself (canonical), the API call that gives the same numbers (api), the license and how to
// attribute the data. Then come the table of the page, with the IDs of its rows, and the notes under it, or the
// items or stages a list links, with their IDs. Its links to other pages are to their views, but for the stages of
// the stage list, which would take about 150,000 characters on CN (October 2026) with links: they are listed by
// their codes and IDs, under the names and IDs of their zones, and the front matter gives the URL of their views
// (stage_url_template), which keeps the view under about 50,000 characters.
//
// A view is made of the same data as its page, never of the frontend's index.html, so it is served whatever the
// shell. It has the status, Cache-Control and Last-Modified of the page, so the same 304s (and no ETag: neither has
// one); its Last-Modified is that of the data alone. The view of an indexable page links the page as its canonical
// in a Link header and is not noindex, which would contradict it; the view of a page that is not indexable is
// noindex as the page is, without a canonical. The views are in no sitemap, and only served at their own paths:
// there is no negotiation by Accept.
//
// A path of a view that is not canonical (/en/result/CN/item/30012.md, a trailing slash, a stage in another zone)
// is redirected to the canonical path of the view, never to the page: a client asking for Markdown gets Markdown.
// Nor is a view ever sent back to the frontend (see unavailable), which has none (its nginx answers 404 to *.md):
// with the pages off, it is a 404.

const (
	mdSuffix     = ".md"
	mimeMarkdown = "text/markdown; charset=utf-8"
)

// hasMarkdown tells whether the pages of kind have a Markdown view
func hasMarkdown(kind pageKind) bool {
	return kind == kindItem || kind == kindItemList || kind == kindStage || kind == kindStageList
}

// mdKind is what the Markdown view of a page of kind is counted as
func mdKind(kind pageKind) pageKind {
	return kind + "_md"
}

// serveMarkdown serves the Markdown view of the page of r, requested at path, as Serve serves the page
func (h *Handler) serveMarkdown(c *fiber.Ctx, r route, path string) {
	if !hasMarkdown(r.kind) {
		b := h.builder(r, nil, nil)
		p := b.notFound()
		h.sendMarkdown(c, nil, nil, p, b.markdown(p))
		return
	}
	if canonical := r.canonicalPath(); path != canonical {
		h.redirect(c, mdKind(r.kind), canonical)
		return
	}
	catalog := h.data.Catalog()
	if catalog == nil {
		h.sendMarkdownCold(c, r)
		return
	}
	matrix := h.data.Matrix(r.server)

	// the lists are the largest, and the same until the data changes
	var key listKey
	if r.kind == kindItemList || r.kind == kindStageList {
		key = listKey{lang: r.lang, server: r.server, kind: r.kind, md: true}
		if e, ok := h.lists.Load(key); ok {
			if e := e.(*listEntry); e.catalog == catalog && e.matrix == matrix {
				h.sendMarkdown(c, catalog, matrix, e.page, e.body)
				return
			}
		}
	}

	p, md, ok := h.buildMarkdown(r, catalog, matrix)
	if !ok {
		count(mdKind(r.kind), "error")
		c.Set(fiber.HeaderContentType, fiber.MIMETextPlainCharsetUTF8)
		c.Set(fiber.HeaderCacheControl, "no-store")
		c.Status(fiber.StatusInternalServerError)
		_ = c.SendString("Internal Server Error\n")
		return
	}
	if p.Redirect != "" {
		h.redirect(c, mdKind(p.Kind), p.Redirect+mdSuffix)
		return
	}
	if key.kind != "" {
		h.lists.Store(key, &listEntry{catalog: catalog, matrix: matrix, page: p, body: md})
	}
	h.sendMarkdown(c, catalog, matrix, p, md)
}

// buildMarkdown builds the page of r and renders its Markdown view; ok is false if that failed
func (h *Handler) buildMarkdown(r route, catalog *service.SEOCatalog, matrix *service.SEOMatrix) (p *Page, md []byte, ok bool) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Error().
				Str("evt.name", "seo.markdown.panic").
				Interface("panic", rec).
				Str("lang", r.lang).Str("server", r.server).Str("kind", string(r.kind)).
				Msg("failed to build the Markdown view of a stats page")
			ok = false
		}
	}()
	b := h.builder(r, catalog, matrix)
	p = b.page()
	if p.Redirect != "" {
		return p, nil, true
	}
	return p, b.markdown(p), true
}

// sendMarkdown sends md, the Markdown view of p, as send sends the page
func (h *Handler) sendMarkdown(c *fiber.Ctx, catalog *service.SEOCatalog, matrix *service.SEOMatrix, p *Page, md []byte) {
	c.Set(fiber.HeaderContentType, mimeMarkdown)
	c.Set(fiber.HeaderContentLanguage, p.Head.Lang)
	if p.Indexable {
		c.Append(fiber.HeaderLink, "<"+p.Head.Canonical+`>; rel="canonical"`)
	}
	h.sendPage(c, mdKind(p.Kind), nil, catalog, matrix, p, md)
}

// sendMarkdownCold answers while the catalog is not loaded, as sendCold does for the page: a 503 to retry later
func (h *Handler) sendMarkdownCold(c *fiber.Ctx, r route) {
	count(mdKind(r.kind), "cold")
	middlewares.ExpectStatus(c)
	c.Set(fiber.HeaderContentType, mimeMarkdown)
	c.Set(fiber.HeaderContentLanguage, r.lang)
	c.Set("X-Robots-Tag", "noindex")
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set(fiber.HeaderRetryAfter, strconv.Itoa(int(retryAfterCold.Seconds())))
	c.Status(fiber.StatusServiceUnavailable)
	_ = c.SendString(mdParagraph(textsByLang[r.lang].Cold) + "\n")
}

// markdownDisabled answers a Markdown view while the pages are off: not found, as the frontend has no views
func markdownDisabled(c *fiber.Ctx, r route) {
	count(mdKind(r.kind), "disabled")
	middlewares.ExpectStatus(c)
	c.Set(fiber.HeaderContentType, fiber.MIMETextPlainCharsetUTF8)
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Status(fiber.StatusNotFound)
	_ = c.SendString("Not Found\n")
}

// markdown renders the Markdown view of p, the page of the request: front matter (but for a 404), the title, then
// the body of the page. Every text is escaped (see mdText): names come from the database.
func (b *builder) markdown(p *Page) []byte {
	var blocks []string
	if p.Status != fiber.StatusNotFound {
		blocks = append(blocks, b.frontMatter(p))
	}
	blocks = append(blocks, "# "+mdText(pageTitle(p)))
	body := p.Body
	if len(body.Servers) > 0 {
		parts := make([]string, len(body.Servers))
		for i, s := range body.Servers {
			if s.Current {
				parts[i] = "**" + mdText(s.Name) + "**"
			} else {
				parts[i] = b.mdLink(s.Link)
			}
		}
		blocks = append(blocks, mdText(body.ServerLabel)+": "+strings.Join(parts, " · "))
	}
	for _, para := range body.Paragraphs {
		blocks = append(blocks, mdParagraph(para))
	}
	if t := body.Table; t != nil {
		blocks = append(blocks, mdParagraph(t.Caption), b.mdTable(t))
	}
	if n := body.Notes; n != nil {
		var line string
		if n.Updated != nil {
			line += mdText(n.UpdatedLabel + " " + n.Updated.Text + n.Stop)
		}
		if n.Data != nil {
			line += "[" + mdText(n.Data.Name) + "](" + mdURL(n.Data.Href) + ")" + mdText(n.Stop)
		}
		line += mdText(n.LicenseLabel) + "[" + mdText(n.LicenseName) + "](" + mdURL(n.LicenseURL) + ")"
		blocks = append(blocks, mdParagraph(n.Definitions), mdLineStart(line))
	}
	for _, g := range body.Groups {
		blocks = append(blocks, "## "+mdText(g.Heading))
		if len(g.Links) > 0 {
			blocks = append(blocks, b.mdList(g.Links, true))
		}
		for _, sub := range g.Subgroups {
			// the stages of the stage list, without links (see above)
			blocks = append(blocks, "### "+mdText(sub.Heading)+" "+mdCode(sub.ID), b.mdList(sub.Links, false))
		}
	}
	if g := body.Related; g != nil {
		blocks = append(blocks, "## "+mdText(g.Heading), b.mdList(g.Links, true))
	}
	if body.Back != nil {
		blocks = append(blocks, b.mdLink(*body.Back))
	}
	return []byte(strings.Join(blocks, "\n\n") + "\n")
}

// pageTitle is the title of p without the name of the site
func pageTitle(p *Page) string {
	return strings.TrimSuffix(p.Head.Title, " | "+p.Head.SiteName)
}

// frontMatter is the YAML front matter of the view of p, a page that exists: what the page is, the data it shows, and
// on which terms. Strings are quoted (see yamlString).
func (b *builder) frontMatter(p *Page) string {
	var sb strings.Builder
	field := func(k, v string) {
		sb.WriteString(k + ": " + v + "\n")
	}
	sb.WriteString("---\n")
	field("title", yamlString(pageTitle(p)))
	field("description", yamlString(p.Head.Description))
	field("lang", b.t.Lang)
	field("server", b.r.server)
	switch b.r.kind {
	case kindItem:
		field("item_id", yamlString(b.r.itemID))
	case kindStage:
		field("zone_id", yamlString(b.r.zoneID))
		field("stage_id", yamlString(b.r.stageID))
	case kindItemList:
		field("items", strconv.Itoa(listed(p.Body.Groups)))
		field("item_url_template", yamlString(b.abs(b.path(b.t.Lang, kindItemList))+"/{item_id}"+mdSuffix))
	case kindStageList:
		field("stages", strconv.Itoa(listed(p.Body.Groups)))
		field("stage_url_template", yamlString(b.abs(b.path(b.t.Lang, kindStageList))+"/{zone_id}/{stage_id}"+mdSuffix))
	}
	if b.matrix != nil && !b.matrix.LastModified.IsZero() {
		field("as_of", b.matrix.LastModified.UTC().Format(time.RFC3339))
	}
	if t := p.Body.Table; t != nil {
		// of the dates of the table
		field("time_zone", b.loc().String())
		field("rows", strconv.Itoa(len(t.Rows)))
		field("rows_total", strconv.Itoa(t.Total))
	}
	field("canonical", yamlString(b.abs(b.r.pagePath())))
	if n := p.Body.Notes; n != nil && n.Data != nil {
		field("api", yamlString(n.Data.Href))
	} else if (b.r.kind == kindItemList || b.r.kind == kindStageList) && b.matrix != nil {
		// the matrix of the server tells which items and stages have rows, the ones the lists link
		field("api", yamlString(b.site+"/PenguinStats/api/v2/result/matrix?server="+b.r.server+"&show_closed_zones=true"))
	}
	field("license", service.DropDataLicenseName)
	field("license_url", service.DropDataLicenseURL)
	host := strings.TrimPrefix(strings.TrimPrefix(b.site, "https://"), "http://")
	field("attribution", yamlString("Data: Penguin Statistics ("+host+"), "+service.DropDataLicenseName))
	sb.WriteString("---")
	return sb.String()
}

// mdTable is t as a Markdown table, with the ID of each row after its head; the numbers are aligned right
func (b *builder) mdTable(t *Table) string {
	var sb strings.Builder
	headers := append([]string{t.Headers[0], "ID"}, t.Headers[1:]...)
	sb.WriteString("|")
	for _, h := range headers {
		sb.WriteString(" " + mdText(h) + " |")
	}
	sb.WriteString("\n|")
	for i := range headers {
		if i < 2 || i == len(headers)-1 {
			sb.WriteString(" --- |")
		} else {
			sb.WriteString(" ---: |")
		}
	}
	for _, row := range t.Rows {
		head := b.mdLink(row.Head)
		if row.HeadExtra != "" {
			head += " " + mdText(row.HeadExtra)
		}
		sb.WriteString("\n| " + head + " | " + mdCode(row.ID) + " |")
		for _, cell := range row.Cells {
			sb.WriteString(" " + mdText(cell) + " |")
		}
		sb.WriteString(" " + mdText(row.Window.text()) + " |")
	}
	return sb.String()
}

// mdList is a list of links, each with the ID of the page it links if any; linked false lists their names alone,
// escaped as paragraphs (see mdParagraph): at the start of an item, a name could start a list or code of its own
func (b *builder) mdList(links []Link, linked bool) string {
	items := make([]string, len(links))
	for i, l := range links {
		if linked {
			items[i] = "- " + b.mdLink(l)
		} else {
			items[i] = "- " + mdParagraph(l.Name)
		}
		if l.ID != "" {
			items[i] += " " + mdCode(l.ID)
		}
	}
	return strings.Join(items, "\n")
}

// listed is the number of links in groups and their subgroups
func listed(groups []Group) int {
	n := 0
	for _, g := range groups {
		n += len(g.Links) + listed(g.Subgroups)
	}
	return n
}

// mdLink is l as a Markdown link, to the view of its page if that has one; its text alone if it links nothing
func (b *builder) mdLink(l Link) string {
	if l.Href == "" {
		return mdText(l.Name)
	}
	return "[" + mdText(l.Name) + "](" + mdURL(b.viewURL(l.Href)) + ")"
}

// viewURL is the URL of the Markdown view of the page at u, or u if that has none
func (b *builder) viewURL(u string) string {
	if path, ok := strings.CutPrefix(u, b.site); ok {
		if r := parseRoute(path); hasMarkdown(r.kind) && !r.md {
			return u + mdSuffix
		}
	}
	return u
}

// mdSpecial are the characters escaped by mdText wherever they are: those of emphasis, code, links, raw HTML (and
// autolinks), table cells and headings (a # ends one), and the backslash
const mdSpecial = "\\`*_[]<>|#"

// mdText escapes s as text of Markdown (CommonMark, and GFM for tables and strikethrough), so that it stays text on
// its line whatever it holds: the characters of mdSpecial and tildes are escaped with a backslash, and line breaks and
// other control characters become spaces. A tilde between two spaces strikes nothing through and is kept as is: the
// time ranges have one in zh and ja. An & is kept too: the entity it may start is shown as the character it stands
// for, as text.
func mdText(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for i, r := range s {
		switch {
		case unicode.IsControl(r):
			sb.WriteByte(' ')
		case strings.ContainsRune(mdSpecial, r):
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case r == '~' && !(i > 0 && s[i-1] == ' ' && i+1 < len(s) && s[i+1] == ' '):
			sb.WriteString(`\~`)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// mdParagraph is s as a paragraph of Markdown: text (see mdText) that does not start a list, a heading, a quote or
// indented code, whose indent may also come from control characters, which mdText turns into spaces
func mdParagraph(s string) string {
	return mdLineStart(strings.TrimLeft(mdText(s), " "))
}

// mdLineStart escapes what starts a list item or a heading at the start of s, a line of text escaped by mdText (which
// escapes the other markers: #, >, *, ~ and `)
func mdLineStart(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '-', '+', '=':
		return `\` + s
	}
	// an ordered list item: up to 9 digits, then . or )
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	if n > 0 && n <= 9 && n < len(s) && (s[n] == '.' || s[n] == ')') {
		return s[:n] + `\` + s[n:]
	}
	return s
}

// mdURL is u as the destination of a Markdown link. The URLs of the pages escape their IDs (url.PathEscape), but
// keep parentheses, which would end the destination.
func mdURL(u string) string {
	return strings.NewReplacer("(", "%28", ")", "%29", " ", "%20", "<", "%3C", ">", "%3E").Replace(u)
}

// mdCode is the ID id as code, which is copied as is, or as text if it holds what code cannot: a backtick, a pipe or
// a backslash (in a table), control characters, or spaces at either end
func mdCode(id string) string {
	if id == "" || strings.ContainsAny(id, "`|\\") || strings.IndexFunc(id, unicode.IsControl) >= 0 || strings.TrimSpace(id) != id {
		return mdText(id)
	}
	return "`" + id + "`"
}

// yamlString is s as a double-quoted YAML string: a JSON string, with < and > escaped, so that a renderer taking
// the front matter for Markdown finds no HTML in it, and so are the characters YAML takes only escaped, which JSON
// keeps (DEL, the C1 controls, U+FFFE and U+FFFF; it escapes the others), and NEL, which YAML 1.1 reads as a line
// break; & is kept, for the URLs
func yamlString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// a string always encodes
	_ = enc.Encode(s)
	var sb strings.Builder
	for _, r := range strings.TrimSuffix(buf.String(), "\n") {
		if r == '<' || r == '>' || unicode.IsControl(r) || r == 0xfffe || r == 0xffff {
			fmt.Fprintf(&sb, `\u%04x`, r)
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
