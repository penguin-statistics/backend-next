// Package guide is the setup guide of the MCP server, at the root of its host: how to add the server to Claude,
// ChatGPT, Cherry Studio, Cursor, VS Code and other apps, what its tools do, its data, limits and privacy. It is in
// English, Simplified Chinese, Japanese and Korean, as HTML for people and as Markdown for agents, with llms.txt (the
// English Markdown, after a preface for agents) and robots.txt for the host.
//
// Everything is rendered once, at startup: the texts (text_en.go, text_zh.go, text_ja.go, text_ko.go, one table per
// language, which a translation adds to) with the snippets and the one-click links (clients.go), all made of the URL
// of the server, into the templates of page.gohtml and page.md.tmpl. The HTML has its style and script inline, and
// loads nothing but its fonts, from its own host (fonts.go), and the icon of the site: its Content-Security-Policy
// allows the style and the script by their hashes. How the page shows the texts is in display.go.
package guide

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	texttemplate "text/template"
	"time"
)

//go:embed page.gohtml page.md.tmpl style.css page.js
var assets embed.FS

// pluginsAvailable shows the plugin and the skill of penguin-statistics/agent-skills (the blocks of the texts marked
// plugin); false hides them, should the repository not be public.
const pluginsAvailable = true

// The paths of the guide besides the root.
const (
	MarkdownPath = "/index.md"
	LLMsPath     = "/llms.txt"
	RobotsPath   = "/robots.txt"
)

// DefaultLang is the language of the guide for a request that asks for none it has.
const DefaultLang = "en"

// texts are the texts of the guide, in the order of the language switch
var texts = []*text{en, zh, ja, ko}

// licenseURL is the license of the data
const licenseURL = "https://creativecommons.org/licenses/by-nc/4.0/"

// Config is what the guide is made of besides its texts.
type Config struct {
	// Endpoint is the public URL of the server, https://<host>/mcp
	Endpoint string
	// Guide is the URL of the guide, the root of the host of Endpoint; Card that of the server card
	Guide, Card string
	// Site is the origin of the website, which has the icon, the API and its documentation
	Site string
	// Repository is the repository of the server
	Repository string
	// RateLimit requests per RateWindow are allowed to a client
	RateLimit  int
	RateWindow time.Duration
}

// Guide is the rendered guide.
type Guide struct {
	// Pages are the pages by language, as ?lang names it
	Pages map[string]*Page
	// LLMs is llms.txt, Robots robots.txt
	LLMs, Robots []byte
	// CSP is the Content-Security-Policy of the HTML pages
	CSP string
	// Files are the fonts of the pages and their license, served on the host of the guide
	Files []File
}

// Page is the guide in a language.
type Page struct {
	// ContentLanguage is the language of the page, as Content-Language tells it
	ContentLanguage string
	HTML, Markdown  []byte
}

// Tools are the tools the guide lists, by name.
func Tools() []string {
	names := make([]string, 0, len(en.Tools))
	for _, t := range en.Tools {
		names = append(names, t.Name)
	}
	return names
}

// unexpanded finds a placeholder left in a rendering: a typo in a text, or a snippet that does not exist
var unexpanded = regexp.MustCompile(`\{[a-z][a-z0-9.-]*\}`)

// Render renders the guide of c.
func Render(c Config) (*Guide, error) {
	if c.Endpoint == "" || c.Guide == "" || c.Card == "" || c.Site == "" || c.Repository == "" {
		return nil, errors.New("guide: the URLs of the server, the guide, the card, the site and the repository are required")
	}
	css, err := assets.ReadFile("style.css")
	if err != nil {
		return nil, err
	}
	js, err := assets.ReadFile("page.js")
	if err != nil {
		return nil, err
	}
	g := &Guide{Pages: make(map[string]*Page, len(texts))}
	// the fonts, by their path
	for _, name := range fonts {
		f, err := fontFile(name)
		if err != nil {
			return nil, err
		}
		if !bytes.Contains(css, []byte("url(fonts/"+name+")")) {
			return nil, fmt.Errorf("guide: style.css does not load %s", name)
		}
		css = bytes.ReplaceAll(css, []byte("url(fonts/"+name+")"), []byte("url("+f.Path+")"))
		g.Files = append(g.Files, f)
	}
	ofl, err := license()
	if err != nil {
		return nil, err
	}
	g.Files = append(g.Files, ofl)
	css, js = []byte(minify(string(css), cssComment)), []byte(minify(string(js), jsComment))
	css = bytes.Replace(css, []byte("{targeted}"), []byte(targeted()), 1)
	g.CSP = "default-src 'none'; style-src '" + hash(css) + "'; script-src '" + hash(js) + "'; font-src 'self'; img-src " +
		c.Site + "; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
	for _, t := range texts {
		v, err := newView(c, t)
		if err != nil {
			return nil, err
		}
		v.CSS, v.JS = htmltemplate.CSS(css), htmltemplate.JS(js) //nolint:gosec // the embedded style.css and page.js
		v.Font = g.Files[0].Path
		p := &Page{ContentLanguage: t.ContentLanguage}
		if p.HTML, err = v.html(); err != nil {
			return nil, err
		}
		if p.Markdown, err = v.markdown(); err != nil {
			return nil, err
		}
		g.Pages[t.Lang] = p
	}
	v, err := newView(c, en)
	if err != nil {
		return nil, err
	}
	v.LLMs = true
	if g.LLMs, err = v.markdown(); err != nil {
		return nil, err
	}
	g.Robots = []byte(v.expand(robots))
	if p := unexpanded.Find(g.Robots); p != nil {
		return nil, fmt.Errorf("guide: robots.txt has an unknown placeholder %s", p)
	}
	return g, nil
}

// robots is robots.txt of the host of the server: the guide and the card may be crawled, and the endpoint, which only
// takes the POSTs of clients, not (Sentry's does the same). The content signals are those of penguin-stats.io.
const robots = `# Penguin Statistics MCP server. Setup guide: {guide}  For agents: {llms}
# Data license: CC BY-NC 4.0. The MCP endpoint (/mcp) takes POST from MCP clients; there is nothing to crawl there.
User-agent: *
Allow: /
Allow: /mcp/server-card
Disallow: /mcp
Content-Signal: search=yes, ai-input=yes, ai-train=yes
`

// targeted are the selectors of the tab of the app whose section is the target of the URL (/#cursor), which shows it
// without the script: one per app, as CSS cannot match the tab of a target otherwise.
func targeted() string {
	var s []string
	for _, id := range slices.Concat(en.Chat, en.Coding, []string{"other"}) {
		s = append(s, `html:not(.tabs) .win:has(#`+id+`:target) [href="#`+id+`"]`)
	}
	return strings.Join(s, ",")
}

// the comments of style.css and of page.js (on lines of their own)
var (
	cssComment = regexp.MustCompile(`(?s)/\*.*?\*/\n?`)
	jsComment  = regexp.MustCompile(`(?m)^\s*//.*$`)
)

// minify leaves the comments and the indentation of the lines of src, CSS or JavaScript, out, as the page does
// without them.
func minify(src string, comment *regexp.Regexp) string {
	src = comment.ReplaceAllString(src, "")
	var lines []string
	for _, line := range strings.Split(src, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// hash is the CSP source of an inline style or script
func hash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

// view is what the templates render: a text with the URLs and links of a configuration.
type view struct {
	T *text
	// r expands the placeholders of the texts
	r *strings.Replacer
	// LLMs renders llms.txt rather than the Markdown of the page
	LLMs bool
	CSS  htmltemplate.CSS
	JS   htmltemplate.JS
	// Font is the path of the font the page preloads
	Font string

	Endpoint, Guide, Card, Site, License string
	// URL is the URL of the page, MarkdownURL and MarkdownPath those of its Markdown, LLMsURL that of llms.txt
	URL, MarkdownURL, MarkdownPath, LLMsURL string
	Langs                                   []langLink
	OneClick                                []oneClick
	Chat, Coding                            []clientView
	// the sections, with their blocks about the plugin left out while it is not available
	Try, Plugin, Data, Limits, Privacy, Troubleshooting, Links section
	Plugins                                                    bool
}

// langLink is a language in the language switch
type langLink struct {
	Name, Abbr, Lang, ContentLanguage string
	// Path is the page in the language, URL its URL, Markdown that of its Markdown
	Path, URL, Markdown string
}

type oneClick struct {
	Label string
	// URL is the link, which can be of the custom scheme of the app (see checkLink)
	URL htmltemplate.URL
}

type clientView struct {
	ID, Summary string
	// Blocks are the blocks of the Markdown, ending with the link to the documentation; the page shows Docs apart
	Blocks []block
	Docs   string
}

func newView(c Config, t *text) (*view, error) {
	v := &view{
		T:            t,
		Endpoint:     c.Endpoint,
		Guide:        c.Guide,
		Card:         c.Card,
		Site:         c.Site,
		License:      licenseURL,
		URL:          c.Guide + "?lang=" + t.Lang,
		MarkdownURL:  strings.TrimSuffix(c.Guide, "/") + MarkdownPath + "?lang=" + t.Lang,
		MarkdownPath: MarkdownPath + "?lang=" + t.Lang,
		LLMsURL:      strings.TrimSuffix(c.Guide, "/") + LLMsPath,
		Plugins:      pluginsAvailable,
	}
	for _, l := range texts {
		v.Langs = append(v.Langs, langLink{Name: l.Name, Abbr: l.Abbr, Lang: l.Lang, ContentLanguage: l.ContentLanguage,
			Path: "/?lang=" + l.Lang, URL: c.Guide + "?lang=" + l.Lang, Markdown: strings.TrimSuffix(c.Guide, "/") + MarkdownPath + "?lang=" + l.Lang})
	}

	window := c.RateWindow.String()
	if c.RateWindow == time.Minute {
		window = t.Minute
	}
	names := Tools()
	pairs := []string{
		"{url}", c.Endpoint, "{guide}", c.Guide, "{md}", v.MarkdownURL, "{llms}", v.LLMsURL, "{card}", c.Card,
		"{site}", c.Site, "{repo}", c.Repository, "{limit}", strconv.Itoa(c.RateLimit), "{window}", window,
		"{tools}", strings.Join(names, ", "), "{toolcount}", strconv.Itoa(len(names)),
	}
	for name, s := range snippets {
		pairs = append(pairs, "{"+name+"}", strings.ReplaceAll(s, "{url}", c.Endpoint))
	}
	for id, cl := range clients {
		pairs = append(pairs, "{"+id+".link}", cl.link(c.Endpoint))
	}
	v.r = strings.NewReplacer(pairs...)

	for _, id := range t.OneClickApps {
		link := clients[id].link(c.Endpoint)
		if err := checkLink(link); err != nil {
			return nil, err
		}
		v.OneClick = append(v.OneClick, oneClick{Label: fmt.Sprintf(t.AddTo, clients[id].name), URL: htmltemplate.URL(link)}) //nolint:gosec // checked
	}
	client := func(id string) clientView {
		ct := t.Clients[id]
		return clientView{ID: id, Summary: ct.Summary, Blocks: append(visible(ct.Blocks), para(t.Docs+ct.Docs)), Docs: ct.Docs}
	}
	for _, id := range t.Chat {
		v.Chat = append(v.Chat, client(id))
	}
	for _, id := range t.Coding {
		v.Coding = append(v.Coding, client(id))
	}
	for _, s := range []struct{ dst, src *section }{
		{&v.Try, &t.Try}, {&v.Plugin, &t.Plugin}, {&v.Data, &t.Data}, {&v.Limits, &t.Limits},
		{&v.Privacy, &t.Privacy}, {&v.Troubleshooting, &t.Troubleshooting}, {&v.Links, &t.Links},
	} {
		*s.dst = section{ID: s.src.ID, Heading: s.src.Heading, Blocks: visible(s.src.Blocks)}
	}
	return v, nil
}

// visible are the blocks shown: those about the plugin only while it is available
func visible(blocks []block) []block {
	shown := make([]block, 0, len(blocks))
	for _, b := range blocks {
		if !b.Plugin || pluginsAvailable {
			shown = append(shown, b)
		}
	}
	return shown
}

func (v *view) expand(s string) string {
	return v.r.Replace(s)
}

// html renders the page as HTML.
func (v *view) html() ([]byte, error) {
	ts := typesetter{lang: v.T.Lang, expand: v.expand, terminal: v.T.Terminal}
	type name struct{ Name, Qualifier string }
	type sentence struct{ First, Rest string }
	t, err := htmltemplate.New("page.gohtml").Funcs(htmltemplate.FuncMap{
		"t": ts.text,
		// a text without its markup and links, which html/template escapes
		"plain": v.expand,
		// the prompt for agents, as it is copied
		"prompt": ts.prompt,
		"blocks": ts.blocks,
		"other":  ts.other,
		"panel": func(c clientView) (panelView, error) {
			n, q := splitName(c.Summary)
			blocks, err := ts.blocks(c.ID, c.Blocks[:len(c.Blocks)-1])
			return panelView{ID: c.ID, Name: n, Qualifier: q, Blocks: blocks, DocsLabel: label(v.T.Docs),
				DocsURL: htmltemplate.URL(c.Docs), DocsText: docsText(c.Docs)}, err //nolint:gosec // a URL of the texts
		},
		// the title, cut before MCP: the name of the site, and MCP Server
		"title": func(s string) [2]string {
			if i := strings.Index(s, " MCP"); i > 0 {
				return [2]string{s[:i], s[i+1:]}
			}
			return [2]string{s, ""}
		},
		// the name of the site in the heading, which may break only between its words if the text has them
		"h1name": func(name string) (htmltemplate.HTML, error) {
			if v.T.TitleWords == nil {
				return htmltemplate.HTML(htmltemplate.HTMLEscapeString(name)), nil //nolint:gosec // escaped
			}
			if strings.Join(v.T.TitleWords, "") != name {
				return "", fmt.Errorf("the words of the title %q do not make its name %q", v.T.TitleWords, name)
			}
			words := make([]string, len(v.T.TitleWords))
			for i, w := range v.T.TitleWords {
				words[i] = htmltemplate.HTMLEscapeString(w)
			}
			return htmltemplate.HTML(strings.Join(words, "<wbr>")), nil //nolint:gosec // escaped
		},
		"name":     func(s string) name { n, q := splitName(s); return name{n, q} },
		"sentence": func(s string) sentence { f, r := splitSentence(s); return sentence{f, r} },
		"badges":   func(s string) []string { return strings.Split(s, " · ") },
		"label":    label,
		// the URL of the server in the narrow column of the apps: without its scheme, which screen readers still read
		// and the button next to it copies, and which may break before its path only
		"shortURL": func(u string) htmltemplate.HTML {
			e := escape(u)
			if rest, ok := strings.CutPrefix(e, "https://"); ok {
				host, path, _ := strings.Cut(rest, "/")
				e = `<span class="sr">https://</span>` + nowrapWords(host)
				if path != "" {
					e += "<wbr>/" + nowrapWords(path)
				}
			}
			return htmltemplate.HTML(e) //nolint:gosec // escaped
		},
		// a copy button of the class class, which the script shows
		"copy": func(class string) htmltemplate.HTML {
			if class != "" {
				class = " " + class
			}
			return htmltemplate.HTML(`<button type="button" class="cp` + class + `" hidden><svg class="i" aria-hidden="true"><use href="#i-copy"/></svg>` + //nolint:gosec // our markup
				copyLabel(class, v.T.Copy) + "</button>")
		},
	}).ParseFS(assets, "page.gohtml")
	if err != nil {
		return nil, err
	}
	return v.execute(t.Execute)
}

// markdown renders the page, or llms.txt, as Markdown.
func (v *view) markdown() ([]byte, error) {
	t, err := texttemplate.New("page.md.tmpl").Funcs(texttemplate.FuncMap{
		"t":    v.expand,
		"code": v.expand,
		"add1": func(i int) int { return i + 1 },
	}).ParseFS(assets, "page.md.tmpl")
	if err != nil {
		return nil, err
	}
	return v.execute(t.Execute)
}

func (v *view) execute(execute func(w io.Writer, data any) error) ([]byte, error) {
	var b bytes.Buffer
	if err := execute(&b, v); err != nil {
		return nil, fmt.Errorf("guide (%s): %w", v.T.Lang, err)
	}
	if p := unexpanded.Find(b.Bytes()); p != nil {
		return nil, fmt.Errorf("guide (%s): unknown placeholder %s", v.T.Lang, p)
	}
	return b.Bytes(), nil
}

// label is s without the colon it ends in: One click: is the label One click
func label(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), ":：")
}

// copyLabel is the text of a copy button: Copy, which only screen readers read on an icon (cp-ico)
func copyLabel(class, copy string) string {
	if strings.Contains(class, "cp-ico") {
		return `<span class="t sr">` + escape(copy) + "</span>"
	}
	return `<span class="t">` + escape(copy) + "</span>"
}
