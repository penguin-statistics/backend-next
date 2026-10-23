package guide

import (
	"bytes"
	"encoding/json"
	"html"
	htmltemplate "html/template"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestTypeset(t *testing.T) {
	en := typesetter{expand: func(s string) string { return s }}
	zh := typesetter{zh: true, expand: en.expand}
	for _, tc := range []struct {
		ts         typesetter
		text, want string
	}{
		// names with hyphens, and the punctuation around them, stay on a line
		{en, "Penguin Statistics (penguin-stats.io) counts", `Penguin Statistics <span class="nw">(penguin-stats.io)</span> counts`},
		{en, "by code (1-7, S2-12)", `by code <span class="nw">(1-7,</span> <span class="nw">S2-12)</span>`},
		// a short code does not break, nor leave its punctuation; where it must (at 360 px), not at a hyphen
		{en, "Keep `\"type\": \"http\"`:", `Keep <span class="nb"><code class="nw">"type": "http"</code>:</span>`},
		{en, "Search `penguin-statistics/agent-skills`,",
			`Search <span class="nb"><code class="nw"><span class="nw">penguin-statistics</span>/<span class="wb"></span><span class="nw">agent-skills</span></code>,</span>`},
		// a URL in code breaks after a slash of its path, its scheme and host only if they are wider than the line
		{en, "Server URL `https://mcp.penguin-stats.io/mcp`.",
			`Server URL <span class="nb"><code><span class="u">https://<wbr>mcp.<span class="nw">penguin-stats</span>.io/</span><wbr>mcp</code>.</span>`},
		// a long one breaks after a slash, a comma or a brace, never at a hyphen
		{en, "`npx skills add penguin-statistics/agent-skills`",
			`<code>npx skills add <span class="nw">penguin-statistics</span>/<wbr><span class="nw">agent-skills</span></code>`},
		{en, "`{\"a\":{\"url\":\"https://x.example/mcp\"},\"b\":\"--transport\"}`",
			`<code>{"a":{<wbr>"url":<span class="u">"https://<wbr>x.example/</span><wbr>mcp"},<wbr>"b":"<span class="nw">--transport</span>"}</code>`},
		// a URL breaks after the slashes of its path, and not after its last
		{en, "Go to https://mcp.penguin-stats.io/ now.",
			`Go to <a href="https://mcp.penguin-stats.io/"><span class="u">https://<wbr>mcp.<span class="nw">penguin-stats</span>.io/</span></a> now.`},
		{en, "See https://github.com/penguin-statistics/agent-skills?tab=readme",
			`See <a href="https://github.com/penguin-statistics/agent-skills?tab=readme"><span class="u">https://<wbr>github.com/</span><wbr><span class="nw">penguin-statistics</span>/<wbr><span class="nw">agent-skills</span><wbr>?tab=readme</a>`},
		// words of other languages in English are marked with theirs
		{en, "Ask in any language: 中文, English, 日本語, 한국어.",
			`Ask in any language: <span lang="zh-CN">中文</span>, English, <span lang="ja">日本語</span>, <span lang="ko">한국어</span>.`},
		{en, "by nickname (狗粮, 石头)", `by nickname (<span lang="zh-CN">狗粮</span>, <span lang="zh-CN">石头</span>)`},
		{en, "[Add to Claude](https://claude.ai/x)", `<a href="https://claude.ai/x">Add to Claude</a>`},
		// the bracket before a link stays with it
		{en, "This page (https://a.example/) is", `This page <span class="nw">(<a href="https://a.example/"><span class="u">https://<wbr>a.example/</span></a></span>) is`},
		// in Chinese, the punctuation after a URL has no space before it, and the last line keeps four characters at least
		{zh, "本页（https://a.example/ ）是说明页。",
			`本页<span class="nw">（<a href="https://a.example/"><span class="u">https://<wbr>a.example/</span></a></span>）是<span class="nw">说明页。</span>`},
		{zh, "填入地址，无需认证", `填入地址，<span class="nw">无需认证</span>`},
		{zh, "粘贴下面的内容后确认：", `粘贴下面的内容<span class="nw">后确认：</span>`},
		{zh, "以 `/mcp` 结尾", `以 <code class="nw">/mcp</code> <span class="nw">结尾</span>`},
		{zh, "认证选 **No sign in**", `认证选 <strong>No sign in</strong>`},
	} {
		got, err := tc.ts.text(tc.text)
		if err != nil || string(got) != tc.want {
			t.Errorf("%q:\n got %s, %v\nwant %s", tc.text, got, err, tc.want)
		}
	}
}

// What the page shows is what the texts say: the text of the page is that of the Markdown, typeset, but for the space
// before Chinese punctuation after a URL, which the Markdown needs only.
func TestTypesetKeepsTheText(t *testing.T) {
	for _, tx := range texts {
		v, err := newView(prod, tx)
		if err != nil {
			t.Fatal(err)
		}
		ts := typesetter{zh: tx.Lang == "zh", expand: v.expand}
		for _, ct := range tx.Clients {
			for _, b := range ct.Blocks {
				for _, s := range slices.Concat([]string{b.Text}, b.Items, b.Steps) {
					want, err := inline(v.expand(s))
					if err != nil {
						t.Fatal(err)
					}
					got, err := ts.text(s)
					if err != nil {
						t.Fatal(err)
					}
					if ts.zh {
						want = htmltemplate.HTML(spacedPunct.ReplaceAllString(string(want), "$1")) //nolint:gosec // a test
					}
					if plainText(got) != plainText(want) {
						t.Errorf("%q: %q", s, plainText(got))
					}
				}
			}
		}
	}
}

func TestCodeBlocks(t *testing.T) {
	g := render(t, prod)
	page := string(g.Pages["en"].HTML)
	label := func(id string) []string {
		section := regexp.MustCompile(`(?s)<section class="panel" id="` + id + `".*?</section>`).FindString(page)
		var labels []string
		for _, m := range regexp.MustCompile(`<span class="cb-l">([^<]*)</span>`).FindAllStringSubmatch(section, -1) {
			labels = append(labels, m[1])
		}
		return labels
	}
	// the file a snippet goes in, from the text before it, Terminal for commands, else its language
	for id, want := range map[string][]string{
		"cursor":        {"~/.cursor/mcp.json"},
		"vscode":        {"~/.copilot/mcp-config.json", "Terminal", ".vscode/mcp.json"},
		"claude-code":   {"Terminal", "Terminal", ".mcp.json"},
		"codex":         {"Terminal", "~/.codex/config.toml"},
		"cherry-studio": {"JSON"},
		"cline":         {"cline_mcp_settings.json", "CLI"},
	} {
		if got := label(id); !slices.Equal(got, want) {
			t.Errorf("%s: labels %q, want %q", id, got, want)
		}
	}
	// a block copies the snippet, the JSON indented
	for _, m := range regexp.MustCompile(`<div class="cb cb-(\w+)" data-c="([^"]*)">`).FindAllStringSubmatch(page, -1) {
		text := html.UnescapeString(m[2])
		switch m[1] {
		case "json":
			var indented bytes.Buffer
			if err := json.Indent(&indented, []byte(text), "", "  "); err != nil || indented.String() != text {
				t.Errorf("JSON %s: %v", text, err)
			}
		case "sh", "toml":
		default:
			t.Errorf("a block of %s", m[1])
		}
		if !strings.Contains(text, prod.Endpoint) && !strings.Contains(text, "penguin-statistics/agent-skills") {
			t.Errorf("%s: %s", m[1], text)
		}
	}
	// the snippets, and the command of Cline
	if n := strings.Count(page, `<div class="cb `); n != 16 {
		t.Errorf("%d blocks of code", n)
	}
	// the text of a block, without the style that makes its lines (textContent, the tools turning HTML into Markdown),
	// is what it copies, line for line
	for lang, p := range g.Pages {
		for _, m := range regexp.MustCompile(`(?s)<div class="cb cb-\w+" data-c="([^"]*)">.*?<pre tabindex="0">(.*?)</pre>`).FindAllStringSubmatch(string(p.HTML), -1) {
			if got, want := plainText(htmltemplate.HTML(m[2])), html.UnescapeString(m[1]); got != want { //nolint:gosec // a test
				t.Errorf("%s: a block reads\n%s\nand copies\n%s", lang, got, want)
			}
		}
	}
}

// Every app has its tab, its section and the link to its documentation, and its tab shows without the script.
func TestPanels(t *testing.T) {
	g := render(t, prod)
	for lang, p := range g.Pages {
		page := string(p.HTML)
		for _, id := range slices.Concat(en.Chat, en.Coding, []string{"other"}) {
			if !strings.Contains(page, `<a class="tab" href="#`+id+`">`) || !strings.Contains(page, `<section class="panel" id="`+id+`"`) ||
				!strings.Contains(page, `html:not(.tabs) .win:has(#`+id+`:target) [href="#`+id+`"]`) {
				t.Errorf("%s: %s has no tab, section or style", lang, id)
			}
		}
		// the label and the link, apart in text too: Docs support.claude.com/…
		docs := regexp.MustCompile(`<p class="docs"><span class="docs-l">[^<]+</span> <a href="(https://[^"]+)"><span>([^<]+)</span>`).FindAllStringSubmatch(page, -1)
		if len(docs) != len(en.Clients) {
			t.Errorf("%s: %d links to documentation", lang, len(docs))
		}
		for _, d := range docs {
			if "https://"+d[2] != d[1] {
				t.Errorf("%s: %s shows %s", lang, d[1], d[2])
			}
		}
	}
}

// Every copy button copies the text it is next to, with no markup.
func TestCopyTargets(t *testing.T) {
	g := render(t, prod)
	for lang, p := range g.Pages {
		page := string(p.HTML)
		targets := regexp.MustCompile(`data-c="([^"]*)"`).FindAllStringSubmatch(page, -1)
		if buttons := strings.Count(page, `<button type="button" class="cp`); buttons != len(targets) || buttons < 30 {
			t.Errorf("%s: %d copy buttons, %d texts to copy", lang, buttons, len(targets))
		}
		for _, m := range targets {
			if c := html.UnescapeString(m[1]); c == "" || strings.ContainsAny(c, "<>`") || strings.Contains(c, "**") {
				t.Errorf("%s: copies %q", lang, c)
			}
		}
	}
}

// The page leads with the prompt for agents: its button, the one filled, is the first control after the title, before
// the URL and the one-click links; it copies the prompt as the Markdown has it, which the page shows as it is copied.
func TestHeroLeadsWithThePrompt(t *testing.T) {
	g := render(t, prod)
	for lang, p := range g.Pages {
		page := string(p.HTML)
		cta := regexp.MustCompile(`<div class="cta" id="agent" role="group" aria-label="[^"]+" data-c="([^"]*)">\n` +
			`<button type="button" class="cp cta-b" hidden><svg class="i" aria-hidden="true"><use href="#i-copy"/></svg><span class="t">([^<]+)</span></button>`).
			FindStringSubmatchIndex(page)
		if cta == nil {
			t.Fatalf("%s: no button for the prompt", lang)
		}
		prompt, label := html.UnescapeString(page[cta[2]:cta[3]]), page[cta[4]:cta[5]]
		if want := map[string]string{"en": "Copy prompt for your agent", "zh": "复制给 AI 的安装指令"}[lang]; label != want {
			t.Errorf("%s: the button reads %q", lang, label)
		}
		if !strings.Contains(string(p.Markdown), "```text\n"+prompt+"\n```") {
			t.Errorf("%s: the prompt differs from the Markdown's: %s", lang, prompt)
		}
		shown := regexp.MustCompile(`<p class="agent-p" id="agent-p">(.*?)</p>`).FindStringSubmatch(page)
		if shown == nil || plainText(htmltemplate.HTML(shown[1])) != prompt { //nolint:gosec // a test
			t.Errorf("%s: the page shows %q, copies %q", lang, shown, prompt)
		}
		if first, card := strings.Index(page, `<button`), strings.Index(page, `<div class="card">`); first != cta[0]+strings.Index(page[cta[0]:], "<button") || first > card {
			t.Errorf("%s: the button for the prompt is not the first", lang)
		}
		if n := strings.Count(page, `class="cp cta-b"`); n != 1 {
			t.Errorf("%s: %d buttons for the prompt", lang, n)
		}
	}
	// no other control is filled: the one-click links are outlined
	if css := string(g.Pages["en"].HTML); strings.Contains(css, ".oc-g .btn:first-child") || strings.Count(css, "background:var(--btn)") != 1 {
		t.Error("a control besides the prompt's button is filled")
	}
	// the Markdown, and llms.txt, give the prompt before the URL and the steps of the apps
	for name, md := range map[string]string{"en": string(g.Pages["en"].Markdown), "zh": string(g.Pages["zh"].Markdown), "llms.txt": string(g.LLMs)} {
		agent, install, url := strings.Index(md, "\n## "+map[bool]string{true: zh.AgentSummary, false: en.AgentSummary}[name == "zh"]+"\n"),
			strings.Index(md, "\n## "+map[bool]string{true: zh.Install, false: en.Install}[name == "zh"]+"\n"), strings.Index(md, "| `"+prod.Endpoint+"` |")
		if agent < 0 || agent > install || install > url {
			t.Errorf("%s: the prompt at %d, the steps at %d, the URL at %d", name, agent, install, url)
		}
	}
}

func TestPageStrings(t *testing.T) {
	for _, tx := range texts {
		for _, s := range []string{tx.SkipLink, tx.PageNav, tx.LangNav, tx.AppNav, tx.Terminal} {
			if s == "" {
				t.Errorf("%s: a string of the page is missing", tx.Lang)
			}
		}
	}
}
