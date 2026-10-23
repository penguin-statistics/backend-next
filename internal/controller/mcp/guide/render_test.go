package guide

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	htmltemplate "html/template"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// prod is the configuration of production
var prod = Config{
	Endpoint:   "https://mcp.penguin-stats.io/mcp",
	Guide:      "https://mcp.penguin-stats.io/",
	Card:       "https://mcp.penguin-stats.io/mcp/server-card",
	Site:       "https://penguin-stats.io",
	Repository: "https://github.com/penguin-statistics/backend-next",
	RateLimit:  1200,
	RateWindow: time.Minute,
}

func render(t *testing.T, c Config) *Guide {
	t.Helper()
	g, err := Render(c)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// The page loads fast on a phone: the HTML, with its style and script, is under 100 KB, 23 KB compressed, and each of
// its two fonts under 20 KB.
func TestPageSize(t *testing.T) {
	g := render(t, prod)
	for lang, p := range g.Pages {
		var gz bytes.Buffer
		w, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
		_, _ = w.Write(p.HTML)
		_ = w.Close()
		if n, z := len(p.HTML), gz.Len(); n >= 100000 || z >= 23000 {
			t.Errorf("%s: %d bytes of HTML, %d gzipped", lang, n, z)
		}
	}
	for _, f := range g.Files {
		if f.ContentType == "font/woff2" && len(f.Body) >= 20000 {
			t.Errorf("%s: %d bytes", f.Path, len(f.Body))
		}
	}
}

// The page loads nothing but its fonts, from its own host, and the icon of the site: no script, style or font of
// another origin, which its CSP would block anyway.
func TestNoExternalResources(t *testing.T) {
	g := render(t, prod)
	files := map[string]bool{}
	for _, f := range g.Files {
		files[f.Path] = true
	}
	for lang, p := range g.Pages {
		html := string(p.HTML)
		if strings.Count(html, "<script") != 1 || !strings.Contains(html, "<script>") || strings.Count(html, "<style") != 1 ||
			strings.Contains(html, `rel="stylesheet"`) || strings.Contains(html, "@import") {
			t.Errorf("%s: a script or a style that is not inline", lang)
		}
		// the fonts are the only URLs of the style
		fonts := regexp.MustCompile(`url\(([^)]*)\)`).FindAllStringSubmatch(html, -1)
		if len(fonts) != 2 {
			t.Errorf("%s: %d url() in the style", lang, len(fonts))
		}
		for _, u := range fonts {
			if !files[u[1]] || !strings.HasPrefix(u[1], FontsPath) {
				t.Errorf("%s: url(%s)", lang, u[1])
			}
		}
		for _, src := range regexp.MustCompile(`\ssrc="([^"]*)"`).FindAllStringSubmatch(html, -1) {
			t.Errorf("%s: src %s", lang, src[1])
		}
		// the icon and the preloaded font are the only <link>s a browser fetches
		preloads := 0
		for _, link := range regexp.MustCompile(`<link rel="([^"]+)"[^>]*href="([^"]+)"`).FindAllStringSubmatch(html, -1) {
			switch link[1] {
			case "icon":
				if !strings.HasPrefix(link[2], prod.Site+"/") {
					t.Errorf("%s: icon %s", lang, link[2])
				}
			case "preload":
				preloads++
				if link[2] != g.Files[0].Path {
					t.Errorf("%s: preload %s", lang, link[2])
				}
			}
		}
		if preloads != 1 || !strings.Contains(html, `as="font" type="font/woff2" crossorigin>`) {
			t.Errorf("%s: %d fonts preloaded", lang, preloads)
		}
		// and the only <use> are of the icons on the page
		for _, use := range regexp.MustCompile(`<use href="([^"]*)"`).FindAllStringSubmatch(html, -1) {
			if !strings.Contains(html, `<symbol id="`+strings.TrimPrefix(use[1], "#")+`"`) {
				t.Errorf("%s: <use href=%s>", lang, use[1])
			}
		}
	}
}

// The fonts are served at a path made of their hash, which changes with them, and their license with them.
func TestFonts(t *testing.T) {
	g := render(t, prod)
	var paths []string
	for _, f := range g.Files {
		paths = append(paths, f.Path)
		switch {
		case f.ContentType == "font/woff2":
			if !regexp.MustCompile(`^/fonts/geist(-mono)?-latin-[0-9a-f]{8}\.woff2$`).MatchString(f.Path) || !f.Immutable ||
				!bytes.HasPrefix(f.Body, []byte("wOF2")) {
				t.Errorf("%s: immutable %v, %.4q", f.Path, f.Immutable, f.Body)
			}
			sum := sha256.Sum256(f.Body)
			if !strings.Contains(f.Path, hex.EncodeToString(sum[:4])) {
				t.Errorf("%s: not the hash of the font", f.Path)
			}
		case f.Path == "/fonts/OFL.txt":
			if f.Immutable || f.ContentType != "text/plain; charset=utf-8" || !bytes.Contains(f.Body, []byte("SIL OPEN FONT LICENSE Version 1.1")) {
				t.Errorf("%s: %s, %.80s", f.Path, f.ContentType, f.Body)
			}
		default:
			t.Errorf("a file %s of %s", f.Path, f.ContentType)
		}
	}
	if len(paths) != 3 || !strings.HasPrefix(paths[0], "/fonts/geist-latin-") {
		t.Errorf("files %v", paths)
	}
}

// The CSP allows the inline style and script by their hashes, and nothing else.
func TestCSPHashes(t *testing.T) {
	g := render(t, prod)
	for lang, p := range g.Pages {
		for _, tc := range []struct{ tag, directive string }{{"style", "style-src"}, {"script", "script-src"}} {
			m := regexp.MustCompile(`(?s)<` + tc.tag + `>(.*?)</` + tc.tag + `>`).FindSubmatch(p.HTML)
			if m == nil {
				t.Fatalf("%s: no <%s>", lang, tc.tag)
			}
			sum := sha256.Sum256(m[1])
			if want := tc.directive + " 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "';"; !strings.Contains(g.CSP, want) {
				t.Errorf("%s: CSP %q lacks %q", lang, g.CSP, want)
			}
		}
	}
	for _, directive := range []string{"default-src 'none'", "font-src 'self';", "img-src https://penguin-stats.io;", "base-uri 'none'",
		"form-action 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(g.CSP, directive) {
			t.Errorf("CSP %q lacks %q", g.CSP, directive)
		}
	}
}

// Every snippet of JSON parses, with the endpoint where it has {url}, and every shell snippet is commands for a shell,
// not for an app (/plugin), which an agent reading llms.txt would fail to run.
func TestSnippets(t *testing.T) {
	for name, s := range snippets {
		if !strings.Contains(s, "{url}") && name != "claude-plugin.sh" {
			t.Errorf("%s does not have the URL", name)
		}
		if langOf(name) == "sh" {
			for _, line := range strings.Split(s, "\n") {
				if strings.HasPrefix(line, "/") {
					t.Errorf("%s: %q is not a shell command", name, line)
				}
			}
		}
		if langOf(name) != "json" {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(strings.ReplaceAll(s, "{url}", prod.Endpoint)), &v); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// and the blocks of code of the pages that are JSON
	for _, p := range render(t, prod).Pages {
		for _, m := range regexp.MustCompile("(?s)```json\n(.*?)\n```").FindAllSubmatch(p.Markdown, -1) {
			if !json.Valid(m[1]) {
				t.Errorf("invalid JSON %s", m[1])
			}
		}
	}
}

// Every one-click link gives the app the endpoint, whatever it is: the configuration (base64, JSON) decodes to it.
func TestLinks(t *testing.T) {
	for _, endpoint := range []string{prod.Endpoint, "https://mcp.example.test:8443/mcp"} {
		for id, c := range clients {
			link := c.link(endpoint)
			if err := checkLink(link); err != nil {
				t.Errorf("%s: %v", id, err)
			}
			u, err := url.Parse(link)
			if err != nil {
				t.Fatalf("%s: %v", id, err)
			}
			q := u.Query()
			var got string
			switch id {
			case "claude":
				got = q.Get("connectorUrl")
			case "goose":
				got = q.Get("url")
			case "cursor", "lmstudio":
				got = decodeJSON(t, id, base64Of(t, q.Get("config")))["url"].(string)
			case "vscode", "vscode-insiders":
				config := decodeJSON(t, id, []byte(q.Get("config")))
				if config["type"] != "http" {
					t.Errorf("%s: type %v", id, config["type"])
				}
				got = config["url"].(string)
			case "cherry-studio":
				server := decodeJSON(t, id, base64Of(t, q.Get("servers")))["mcpServers"].(map[string]any)["penguin-stats"].(map[string]any)
				if server["type"] != "streamableHttp" {
					t.Errorf("%s: type %v", id, server["type"])
				}
				got = server["url"].(string)
			default:
				t.Errorf("%s: a link this test does not check", id)
			}
			if got != endpoint {
				t.Errorf("%s: %s gives %q, want %q", id, link, got, endpoint)
			}
		}
	}
	// the format that was checked, for production
	if got := clients["cursor"].link(prod.Endpoint); got != "https://cursor.com/en/install-mcp?name=penguin-stats&config=eyJ1cmwiOiJodHRwczovL21jcC5wZW5ndWluLXN0YXRzLmlvL21jcCJ9" {
		t.Errorf("cursor: %s", got)
	}
	if got := clients["cherry-studio"].link(prod.Endpoint); got != "cherrystudio://mcp/install?servers=eyJtY3BTZXJ2ZXJzIjp7InBlbmd1aW4tc3RhdHMiOnsidHlwZSI6InN0cmVhbWFibGVIdHRwIiwidXJsIjoiaHR0cHM6Ly9tY3AucGVuZ3Vpbi1zdGF0cy5pby9tY3AifX19" {
		t.Errorf("cherry-studio: %s", got)
	}
}

func base64Of(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("%q: %v", s, err)
	}
	return b
}

func decodeJSON(t *testing.T, id string, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v: %s", id, err, b)
	}
	return m
}

// Every URL of the server in the guide is made of its configuration: a guide for another host names that host only.
func TestEverythingFromTheConfiguration(t *testing.T) {
	c := Config{Endpoint: "https://mcp.example.test/mcp", Guide: "https://mcp.example.test/", Card: "https://mcp.example.test/mcp/server-card",
		Site: "https://site.example.test", Repository: "https://github.com/example/backend", RateLimit: 30, RateWindow: 10 * time.Second}
	g := render(t, c)
	files := map[string][]byte{"llms.txt": g.LLMs, "robots.txt": g.Robots}
	for lang, p := range g.Pages {
		files[lang+".html"], files[lang+".md"] = p.HTML, p.Markdown
	}
	for name, b := range files {
		// the hosts of the server and of the site (the documentation of the API, on its own host, stays)
		for _, host := range []string{"mcp.penguin-stats.io", "//penguin-stats.io"} {
			if i := bytes.Index(b, []byte(host)); i >= 0 {
				t.Errorf("%s: a URL of production: %s", name, b[max(0, i-40):min(len(b), i+40)])
			}
		}
		if name != "robots.txt" && !bytes.Contains(b, []byte("https://mcp.example.test/mcp")) {
			t.Errorf("%s does not have the endpoint", name)
		}
	}
	if !bytes.Contains(g.Pages["en"].HTML, []byte("30 requests per 10s")) || !bytes.Contains(g.Pages["zh"].Markdown, []byte("每10s 30 次请求")) {
		t.Error("the rate limit is not that of the configuration")
	}
}

func TestRobots(t *testing.T) {
	robots := string(render(t, prod).Robots)
	for _, line := range []string{"User-agent: *", "Allow: /", "Allow: /mcp/server-card", "Disallow: /mcp",
		"Content-Signal: search=yes, ai-input=yes, ai-train=yes"} {
		if !slices.Contains(strings.Split(robots, "\n"), line) {
			t.Errorf("no %q in %s", line, robots)
		}
	}
}

// llms.txt is the English Markdown for agents: the preface first, and no one-click buttons, which agents cannot use.
func TestLLMs(t *testing.T) {
	g := render(t, prod)
	llms := string(g.LLMs)
	if !strings.HasPrefix(llms, "# Penguin Statistics MCP Server\n\n> Read-only remote MCP server") ||
		!strings.Contains(llms, "list the tools (search_items, search_stages, get_item_drop_rates, get_stage_drops, list_events, get_dataset_info)") ||
		!strings.Contains(llms, "· 6 tools.") {
		t.Errorf("preface: %.600s", llms)
	}
	if strings.Contains(llms, "**One click:**") || !strings.Contains(string(g.Pages["en"].Markdown), "**One click:**") {
		t.Error("the one-click buttons are in llms.txt, or not in the Markdown")
	}
	if !strings.Contains(llms, "## Install") || !strings.Contains(llms, "#### Cursor") {
		t.Errorf("llms.txt lacks the steps: %.300s", llms)
	}
}

// The languages have the same sections, apps, tools and blocks of code, in the order of each, and only apps with a
// one-click link in the one-click row.
func TestTextsMatch(t *testing.T) {
	keys := func(m map[string]clientText) []string {
		var k []string
		for id := range m {
			k = append(k, id)
		}
		sort.Strings(k)
		return k
	}
	for _, tx := range texts {
		if got := keys(tx.Clients); !slices.Equal(got, keys(en.Clients)) {
			t.Errorf("%s: apps %v", tx.Lang, got)
		}
		listed := slices.Concat(tx.Chat, tx.Coding)
		sort.Strings(listed)
		if !slices.Equal(listed, keys(en.Clients)) {
			t.Errorf("%s: apps listed %v", tx.Lang, listed)
		}
		for _, id := range tx.OneClickApps {
			if _, ok := clients[id]; !ok {
				t.Errorf("%s: %s has no one-click link", tx.Lang, id)
			}
		}
		var names []string
		for _, tl := range tx.Tools {
			names = append(names, tl.Name)
		}
		if !slices.Equal(names, Tools()) {
			t.Errorf("%s: tools %v", tx.Lang, names)
		}
		for id, ct := range tx.Clients {
			if code, want := codeOf(ct.Blocks), codeOf(en.Clients[id].Blocks); !slices.Equal(code, want) {
				t.Errorf("%s %s: code %v, want %v", tx.Lang, id, code, want)
			}
		}
		if len(tx.Other.Rows) != len(en.Other.Rows) {
			t.Errorf("%s: %d other apps", tx.Lang, len(tx.Other.Rows))
		}
	}
}

func codeOf(blocks []block) []string {
	var code []string
	for _, b := range blocks {
		if b.Code != "" {
			code = append(code, b.Code)
		}
	}
	return code
}

// The anchors of the page are unique, and those other sites link to (the privacy section, for directories) stay.
func TestAnchors(t *testing.T) {
	for lang, p := range render(t, prod).Pages {
		seen := map[string]bool{}
		for _, m := range regexp.MustCompile(` id="([^"]+)"`).FindAllSubmatch(p.HTML, -1) {
			if seen[string(m[1])] {
				t.Errorf("%s: id %s twice", lang, m[1])
			}
			seen[string(m[1])] = true
		}
		for _, id := range []string{"install", "claude", "chatgpt", "cherry-studio", "claude-code", "cursor", "vscode", "codex",
			"gemini-cli", "trae", "cline", "other", "try", "plugin", "tools", "data", "limits", "privacy", "troubleshooting", "links"} {
			if !seen[id] {
				t.Errorf("%s: no id %s", lang, id)
			}
		}
	}
}

// The page says what the server is and how to reach it, in the language of the request.
func TestPageContents(t *testing.T) {
	g := render(t, prod)
	for lang, want := range map[string][]string{
		"en": {`<html lang="en" data-copied="Copied" id="top">`, "<title>Penguin Statistics MCP Server</title>",
			`<p class="lbl" id="url">Server URL</p>` + "\n" + `<div class="url" role="group" aria-labelledby="url" data-c="https://mcp.penguin-stats.io/mcp"><code>https://mcp.penguin-stats.io/mcp</code>`,
			`<h1 id="title"><span>Penguin Statistics</span> <span class="h1-b">MCP Server</span></h1>`,
			`<link rel="alternate" type="text/markdown" href="https://mcp.penguin-stats.io/index.md?lang=en">`,
			`<link rel="service-desc" type="application/mcp-server-card+json" href="https://mcp.penguin-stats.io/mcp/server-card">`,
			`<link rel="alternate" hreflang="zh-CN" href="https://mcp.penguin-stats.io/?lang=zh">`,
			`<meta property="og:image" content="https://penguin-stats.io/favicon/android-chrome-512x512.png">`,
			"Logs are kept for 15 days", "CC BY-NC 4.0", "1200 requests per minute"},
		"zh": {`<html lang="zh-CN" data-copied="已复制" id="top">`, "<title>企鹅物流数据统计 MCP 服务器</title>",
			`<h1 id="title"><span>企鹅物流数据统计</span> <span class="h1-b">MCP 服务器</span></h1>`, "保存 15 天", "每分钟 1200 次请求"},
	} {
		html := string(g.Pages[lang].HTML)
		for _, s := range want {
			if !strings.Contains(html, s) {
				t.Errorf("%s: no %s", lang, s)
			}
		}
		// the credit, as text (the page lets it break after its comma)
		if text := plainText(htmltemplate.HTML(html)); !strings.Contains(text, "Data: Penguin Statistics (penguin-stats.io), CC BY-NC 4.0") {
			t.Errorf("%s: no credit line", lang)
		}
		// the data is CC BY-NC 4.0, with no other terms
		if strings.Contains(html, "ask us first") || strings.Contains(html, "商业用途请先联系") {
			t.Errorf("%s: terms beyond the license", lang)
		}
		if strings.Contains(html, "**") || strings.Contains(html, "`") {
			t.Errorf("%s: markup left in the HTML", lang)
		}
		// the line for agents, which screens do not show, has nothing to focus
		if sr := regexp.MustCompile(`<p class="sr">(.*?)</p>`).FindStringSubmatch(html); sr == nil ||
			!strings.Contains(sr[1], "https://mcp.penguin-stats.io/llms.txt") || strings.Contains(sr[1], "<") {
			t.Errorf("%s: the line for agents is %q", lang, sr)
		}
	}
	// the zh page opens with Cherry Studio, the en one with Claude
	if zh := string(g.Pages["zh"].HTML); strings.Index(zh, `id="cherry-studio"`) > strings.Index(zh, `id="claude"`) {
		t.Error("zh: Cherry Studio is not first")
	}
	if en := string(g.Pages["en"].HTML); strings.Index(en, `id="claude"`) > strings.Index(en, `id="cherry-studio"`) {
		t.Error("en: Claude is not first")
	}
}
