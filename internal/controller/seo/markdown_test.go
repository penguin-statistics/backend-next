package seo

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/spashell"
	"exusiai.dev/backend-next/internal/service"
)

// frontMatter parses the front matter of a Markdown view into its fields, the quoted ones unquoted, and returns the
// rest of the view
func frontMatter(t *testing.T, body string) (fields map[string]string, rest string) {
	t.Helper()
	block, rest, ok := strings.Cut(strings.TrimPrefix(body, "---\n"), "\n---\n")
	if !ok || !strings.HasPrefix(body, "---\n") {
		t.Fatalf("no front matter:\n%s", body)
	}
	fields = map[string]string{}
	for _, line := range strings.Split(block, "\n") {
		k, v, ok := strings.Cut(line, ": ")
		if !ok || k == "" || strings.ContainsAny(k, " \"") {
			t.Fatalf("front matter line %q", line)
		}
		if _, dup := fields[k]; dup {
			t.Errorf("front matter: %s twice", k)
		}
		if strings.HasPrefix(v, `"`) {
			// the quoted strings are JSON strings, which YAML reads the same
			if err := json.Unmarshal([]byte(v), &v); err != nil {
				t.Fatalf("front matter %s: %v", k, err)
			}
		} else if strings.ContainsAny(v, `:#'"[]{}&*!|>%@`+"`") && k != "as_of" && !strings.HasPrefix(v, "https://") {
			t.Errorf("front matter %s: %q unquoted", k, v)
		}
		fields[k] = v
	}
	return fields, rest
}

var mdEscapedRe = regexp.MustCompile(`\\([!-/:-@\[-` + "`" + `{-~])`)

// mdUnescape undoes the backslash escapes of Markdown text
func mdUnescape(s string) string {
	return mdEscapedRe.ReplaceAllString(s, "$1")
}

var mdLinkRe = regexp.MustCompile(`^\[((?:[^\]\\]|\\.)*)\]\(([^)]*)\)`)

// mdRows parses the rows of the table of a Markdown view into their cells, as written
func mdRows(t *testing.T, body string) [][]string {
	t.Helper()
	var rows [][]string
	inTable := false
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "| ") {
			inTable = false
			continue
		}
		if strings.HasPrefix(line, "| --- |") {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		var cells []string
		cell, escaped := "", false
		for _, r := range strings.TrimSuffix(strings.TrimPrefix(line, "| "), " |") {
			switch {
			case escaped:
				escaped = false
			case r == '\\':
				escaped = true
			case r == '|':
				cells = append(cells, strings.TrimSpace(cell))
				cell = ""
				continue
			}
			cell += string(r)
		}
		rows = append(rows, append(cells, strings.TrimSpace(cell)))
	}
	return rows
}

var (
	htmlRowRe  = regexp.MustCompile(`<tr><th scope="row">(.*?)</th>(.*?)</tr>`)
	htmlCellRe = regexp.MustCompile(`<td>(.*?)</td>`)
	htmlTagRe  = regexp.MustCompile(`<[^>]*>`)
)

// htmlRows parses the rows of the table of a page into their text: the head, then the cells
func htmlRows(body string) [][]string {
	var rows [][]string
	for _, m := range htmlRowRe.FindAllStringSubmatch(ssrRe.FindString(body), -1) {
		row := []string{html.UnescapeString(htmlTagRe.ReplaceAllString(m[1], ""))}
		for _, c := range htmlCellRe.FindAllStringSubmatch(m[2], -1) {
			row = append(row, html.UnescapeString(htmlTagRe.ReplaceAllString(c[1], "")))
		}
		rows = append(rows, row)
	}
	return rows
}

func TestMarkdownItemView(t *testing.T) {
	e := newTestEnv(t)
	r := e.get(t, "/en/result/cn/item/30012.md")
	if r.status != 200 {
		t.Fatalf("status %d", r.status)
	}
	for k, want := range map[string]string{
		"Content-Type":     "text/markdown; charset=utf-8",
		"Content-Language": "en",
		"Cache-Control":    "public, max-age=60, s-maxage=300",
		"Last-Modified":    "Wed, 21 Oct 2026 10:30:00 GMT",
		"Link":             `<https://penguin-stats.io/en/result/cn/item/30012>; rel="canonical"`,
		// the canonical says where to index it: noindex would contradict it
		"X-Robots-Tag": "",
		"ETag":         "",
		"Vary":         "",
		// as the page
		"Cross-Origin-Embedder-Policy": "",
		"Access-Control-Allow-Origin":  "",
	} {
		if got := r.header.Get(k); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
	}
	fm, rest := frontMatter(t, r.body)
	for k, want := range map[string]string{
		"title":       "Orirock Cube Drop Rates (CN server)",
		"lang":        "en",
		"server":      "CN",
		"item_id":     "30012",
		"as_of":       "2026-10-21T10:30:00Z",
		"time_zone":   "UTC+8",
		"rows":        "3",
		"rows_total":  "3",
		"canonical":   "https://penguin-stats.io/en/result/cn/item/30012",
		"api":         "https://penguin-stats.io/PenguinStats/api/v2/result/matrix?server=CN&itemFilter=30012&show_closed_zones=true",
		"license":     "CC BY-NC 4.0",
		"license_url": "https://creativecommons.org/licenses/by-nc/4.0/",
		"attribution": "Data: Penguin Statistics (penguin-stats.io), CC BY-NC 4.0",
	} {
		if fm[k] != want {
			t.Errorf("front matter %s: %q, want %q", k, fm[k], want)
		}
	}
	if !strings.HasPrefix(fm["description"], "Orirock Cube drop rates on the CN server: 1-7 124.56% (1,061,347 samples), ") {
		t.Errorf("description %q", fm["description"])
	}
	// the rows in the order of the page, linking the views of the stages
	var stages []string
	for _, row := range mdRows(t, rest) {
		stages = append(stages, row[1])
	}
	if strings.Join(stages, ",") != "`act1_02`,`act1_01`,`main_01-07`" {
		t.Errorf("rows %v", stages)
	}
	assertOnce(t, rest,
		"| [1-7](https://penguin-stats.io/en/result/cn/stage/main_1/main_01-07.md) Evil Time Part 1 | `main_01-07` | 1322056 | 1061347 | 124.56% | 4.82 | 6 | 2019-05-01 until now |",
		"[US server](https://penguin-stats.io/en/result/us/item/30012.md)",
		"- [Orirock](https://penguin-stats.io/en/result/cn/item/30011.md)",
	)
	golden(t, "testdata/item_en_cn.golden.md", r.body)
}

func TestMarkdownStageView(t *testing.T) {
	e := newTestEnv(t)
	r := e.get(t, "/zh/result/cn/stage/main_1/main_01-07.md")
	if r.status != 200 || r.header.Get("Content-Language") != "zh" ||
		r.header.Get("Link") != `<https://penguin-stats.io/zh/result/cn/stage/main_1/main_01-07>; rel="canonical"` {
		t.Fatalf("status %d, Content-Language %q, Link %q", r.status, r.header.Get("Content-Language"), r.header.Get("Link"))
	}
	fm, _ := frontMatter(t, r.body)
	if fm["zone_id"] != "main_1" || fm["stage_id"] != "main_01-07" || fm["item_id"] != "" || fm["title"] != "1-7 黑暗时代·上 掉落统计（国服）" ||
		fm["api"] != "https://penguin-stats.io/PenguinStats/api/v2/result/matrix?server=CN&stageFilter=main_01-07&show_closed_zones=true" {
		t.Errorf("front matter %v", fm)
	}
	golden(t, "testdata/stage_zh_cn.golden.md", r.body)

	// a closed time range in the words of each language: its tilde strikes nothing through
	for lang, want := range map[string]string{
		"zh": "| 2023-06-29 ~ 2023-07-12 |",
		"ja": "| 2023-06-29 ~ 2023-07-12 |",
		"ko": "| 2023-06-29 - 2023-07-12 |",
	} {
		if r := e.get(t, "/"+lang+"/result/cn/stage/act1_zone1/act1_02.md"); !strings.Contains(r.body, want) {
			t.Errorf("%s: no %s", lang, want)
		}
	}
}

// The views show what the pages show: the same rows with the same numbers, the same status and indexing
func TestMarkdownShowsTheDataOfThePage(t *testing.T) {
	e := newTestEnv(t)
	warmAll(e)
	paths := []string{
		"/item/30011", "/item/30012", "/item/30013", "/item/evil", "/item/quiet", "/item/tag_1", "/item/nope",
		"/stage/main_1/main_01-07", "/stage/act1_zone1/act1_01", "/stage/act1_zone1/act1_02", "/stage/recruit/recruit",
		"/stage/main_1/nope",
		"/item", "/stage",
	}
	tables := 0
	for _, lang := range service.StatsLangs {
		for _, server := range []string{"cn", "us", "jp", "kr"} {
			for _, p := range paths {
				path := "/" + lang + "/result/" + server + p
				page, view := e.get(t, path), e.get(t, path+".md")
				if page.status != view.status || page.header.Get("X-Robots-Tag") != view.header.Get("X-Robots-Tag") ||
					page.header.Get("Cache-Control") != view.header.Get("Cache-Control") ||
					page.header.Get("Last-Modified") != view.header.Get("Last-Modified") {
					t.Errorf("%s: page %d (X-Robots-Tag %q, Cache-Control %q, Last-Modified %q), view %d (%q, %q, %q)", path,
						page.status, page.header.Get("X-Robots-Tag"), page.header.Get("Cache-Control"), page.header.Get("Last-Modified"),
						view.status, view.header.Get("X-Robots-Tag"), view.header.Get("Cache-Control"), view.header.Get("Last-Modified"))
				}
				// the canonical of the page, if it has one, and that only
				var canonical string
				if m := canonicalRe.FindStringSubmatch(page.body); m != nil {
					canonical = `<` + m[1] + `>; rel="canonical"`
				}
				if view.header.Get("Link") != canonical {
					t.Errorf("%s: Link %q, want %q", path, view.header.Get("Link"), canonical)
				}
				want, got := htmlRows(page.body), mdRows(t, view.body)
				if len(got) != len(want) {
					t.Errorf("%s: %d rows, want %d", path, len(got), len(want))
					continue
				}
				if len(want) > 0 {
					tables++
				}
				for i := range want {
					head := got[i][0]
					if m := mdLinkRe.FindStringSubmatch(head); m != nil {
						head = m[1] + head[len(m[0]):]
					}
					// the ID after the head
					cells := append([]string{mdUnescape(head)}, got[i][2:]...)
					for j := 1; j < len(cells); j++ {
						cells[j] = mdUnescape(cells[j])
					}
					if strings.Join(cells, "\x00") != strings.Join(want[i], "\x00") {
						t.Errorf("%s row %d:\n%q\nwant\n%q", path, i, cells, want[i])
					}
				}
			}
		}
	}
	if tables < 10 {
		t.Errorf("%d tables compared", tables)
	}

	// the lists list what the list pages link, in the same order
	pageLinkRe := regexp.MustCompile(`<li><a href="[^"]*/(?:item|stage/[^/"]+)/([^/"]+)">`)
	viewIDRe := regexp.MustCompile("(?m)^- .* `([^`]+)`$")
	for _, path := range []string{"/en/result/cn/item", "/ja/result/us/item", "/zh/result/cn/stage", "/ko/result/us/stage"} {
		var want, got []string
		for _, m := range pageLinkRe.FindAllStringSubmatch(ssrRe.FindString(e.get(t, path).body), -1) {
			want = append(want, m[1])
		}
		for _, m := range viewIDRe.FindAllStringSubmatch(e.get(t, path+".md").body, -1) {
			got = append(got, m[1])
		}
		if len(want) == 0 || strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: view lists %v, page links %v", path, got, want)
		}
	}
}

func TestMarkdownEscapesNames(t *testing.T) {
	e := newTestEnv(t)
	for _, path := range []string{
		"/en/result/cn/item/evil.md",
		"/en/result/cn/stage/act1_zone1/act1_01.md",
		"/ja/result/cn/item/30012.md",
		"/zh/result/cn/stage/act1_zone1/act1_02.md",
	} {
		r := e.get(t, path)
		if r.status != 200 {
			t.Errorf("%s: status %d", path, r.status)
			continue
		}
		fm, rest := frontMatter(t, r.body)
		if strings.ContainsAny(strings.Join(strings.Split(r.body, "\n")[:len(fm)+2], "\n"), "<>") {
			t.Errorf("%s: < or > in the front matter", path)
		}
		// no markup but ours: every < > [ ] | ` * _ of a name is escaped
		unescaped := regexp.MustCompile(`(^|[^\\])(\\\\)*[<>*_` + "`" + `]`)
		for _, line := range strings.Split(rest, "\n") {
			// ours: the IDs as code, the destinations of the links and the current server in bold
			line = regexp.MustCompile("`[A-Za-z0-9_-]+`|\\]\\([^)]*\\)|\\*\\*").ReplaceAllString(line, "")
			if m := unescaped.FindString(line); m != "" {
				t.Errorf("%s: unescaped %q in %q", path, m, line)
			}
		}
		if !strings.Contains(rest, `\</script\>\<img src=x onerror=alert(1)\>"'&`) {
			t.Errorf("%s: the hostile name is not shown escaped", path)
		}
		if strings.HasSuffix(path, "/item/evil.md") && fm["title"] != hostile+" Drop Rates (CN server)" {
			t.Errorf("%s: title %q", path, fm["title"])
		}
		if strings.HasSuffix(path, "/act1_01.md") && fm["title"] != hostile+" Drop Rates (CN server)" {
			t.Errorf("%s: title %q", path, fm["title"])
		}
	}
}

// yamlPrintable tells whether YAML takes r as it is in a document (c-printable)
func yamlPrintable(r rune) bool {
	return r == '\t' || r == '\n' || r == '\r' || r >= 0x20 && r <= 0x7e || r == 0x85 || r >= 0xa0 && r <= 0xd7ff ||
		r >= 0xe000 && r <= 0xfffd || r >= 0x10000 && r <= 0x10ffff
}

// mdBlockStartRe matches the start of a line that would make a block of its own in Markdown (a list, indented code,
// a quote or a setext underline), once the marker of a list item of the view ("- ") is stripped
var mdBlockStartRe = regexp.MustCompile(`^([ \t]|\d{1,9}[.)]([ \t]|$)|[-+*>=])`)

// The names come from the database: control characters and what would start a block at the start of a line stay text
func TestMarkdownControlCharacters(t *testing.T) {
	e := newTestEnv(t)
	const name = "\x01   1. Ori\x7frock\u0085Cube\u009f\ufffe"
	catalog := testCatalog()
	for _, lang := range service.StatsLangs {
		catalog.Items["30012"].Name[lang] = name
		catalog.Stages["main_01-07"].Code[lang] = "\n    - 1-7"
		catalog.Stages["act1_02"].Code[lang] = "1. x"
	}
	e.data.catalog = catalog
	for _, path := range []string{
		"/en/result/cn/item/30012.md",
		"/zh/result/cn/item/30012.md",
		"/en/result/cn/stage/main_1/main_01-07.md",
		"/zh/result/cn/stage/main_1/main_01-07.md",
		"/en/result/cn/item/30011.md",
		"/en/result/cn/item.md",
		"/en/result/cn/stage.md",
		"/zh/result/cn/stage.md",
	} {
		r := e.get(t, path)
		if r.status != 200 {
			t.Errorf("%s: status %d", path, r.status)
			continue
		}
		fm, rest := frontMatter(t, r.body)
		block := strings.TrimSuffix(r.body, rest)
		if i := strings.IndexFunc(block, func(r rune) bool { return !yamlPrintable(r) }); i >= 0 {
			t.Errorf("%s: %q in the front matter, which YAML does not take", path, []rune(block[i:])[0])
		}
		if strings.HasSuffix(path, "/en/result/cn/item/30012.md") && fm["title"] != name+" Drop Rates (CN server)" {
			t.Errorf("%s: title %q", path, fm["title"])
		}
		for _, line := range strings.Split(rest, "\n") {
			if mdBlockStartRe.MatchString(strings.TrimPrefix(line, "- ")) {
				t.Errorf("%s: %q starts a block", path, line)
			}
		}
	}
	// the stages of the stage list, which it does not link
	assertOnce(t, e.get(t, "/en/result/cn/stage.md").body, "\n- \\- 1-7 `main_01-07`\n", "\n- 1\\. x `act1_02`\n")
}

func TestMarkdownEscaping(t *testing.T) {
	for in, want := range map[string]string{
		"Orirock Cube":            "Orirock Cube",
		"a|b":                     `a\|b`,
		"*x* _y_ `z` [l](u) <h>":  `\*x\* \_y\_ \` + "`" + `z\` + "`" + ` \[l\](u) \<h\>`,
		`back\slash`:              `back\\slash`,
		"# heading #":             `\# heading \#`,
		"~~strike~~":              `\~\~strike\~\~`,
		"2023-06-29 ~ 2023-07-12": "2023-06-29 ~ 2023-07-12",
		"line\nbreak\r\tand\x00":  "line break  and ",
		"&lt; & &amp;":            "&lt; & &amp;",
		"固源岩 · 黑暗时代":              "固源岩 · 黑暗时代",
	} {
		if got := mdText(in); got != want {
			t.Errorf("mdText(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"- item":        `\- item`,
		"+ item":        `\+ item`,
		"1. item":       `1\. item`,
		"12) item":      `12\) item`,
		"1234567890. x": "1234567890. x",
		"= setext":      `\= setext`,
		"> quote":       `\> quote`,
		"# heading":     `\# heading`,
		"    code":      "code",
		"\x01    code":  "code",
		"\n\t- item":    `\- item`,
		"1-7 stage":     "1-7 stage",
	} {
		if got := mdParagraph(in); got != want {
			t.Errorf("mdParagraph(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"main_01-07": "`main_01-07`",
		"a`b":        "a\\`b",
		"a|b":        `a\|b`,
		" a":         " a",
		"":           "",
	} {
		if got := mdCode(in); got != want {
			t.Errorf("mdCode(%q) = %q, want %q", in, got, want)
		}
	}
	if got := mdURL("https://x/a(b)/c d"); got != "https://x/a%28b%29/c%20d" {
		t.Errorf("mdURL: %q", got)
	}
	for in, want := range map[string]string{
		"a \"b\" <c> & \\ \n":                           `"a \"b\" \u003cc\u003e & \\ \n"`,
		"\x01\x7f\u0085\u009f\u00a0é\u2028\ufffe\uffff": "\"\\u0001\\u007f\\u0085\\u009f\u00a0é\\u2028\\ufffe\\uffff\"",
	} {
		if got := yamlString(in); got != want {
			t.Errorf("yamlString(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestMarkdownNotFound(t *testing.T) {
	e := newTestEnv(t)
	for _, path := range []string{
		"/zh/result/cn/item/nope.md",
		"/zh/result/cn/item/tag_1.md",
		"/zh/result/us/item/30013.md",
		"/en/result/cn/stage/main_1/nope.md",
		"/en/result/kr/stage/act1_zone1/act1_01.md",
		// no page, or no view of it
		"/en/result/cn.md",
		"/en/result/cn/foo.md",
		"/en/result/cn/item/30012/more.md",
		"/en/result/xx/item/30012.md",
		"/en/result/xx/item.md",
		"/en/result/cn/stage/main_1.md",
		// an ID .md
		"/en/result/cn/item/.md",
	} {
		r := e.get(t, path)
		if r.status != 404 || r.header.Get("X-Robots-Tag") != "noindex" || r.header.Get("Cache-Control") != "public, max-age=60" ||
			r.header.Get("Link") != "" || r.header.Get("Last-Modified") != "" {
			t.Errorf("%s: status %d, X-Robots-Tag %q, Cache-Control %q, Link %q", path, r.status,
				r.header.Get("X-Robots-Tag"), r.header.Get("Cache-Control"), r.header.Get("Link"))
			continue
		}
		if ct := r.header.Get("Content-Type"); ct != "text/markdown; charset=utf-8" && !strings.HasSuffix(path, "/item/.md") {
			t.Errorf("%s: Content-Type %q", path, ct)
		}
	}
	r := e.get(t, "/ja/result/cn/stage/main_1/nope.md")
	if r.body != "# ページが見つかりません\n\nページが見つかりません：アイテムまたはステージが存在しないか、このサーバーにはありません。\n\n"+
		"[作戦別（大陸版）](https://penguin-stats.io/ja/result/cn/stage.md)\n" {
		t.Errorf("body %q", r.body)
	}
}

func TestMarkdownNoindexPages(t *testing.T) {
	e := newTestEnv(t)
	for _, path := range []string{"/en/result/cn/item/quiet.md", "/en/result/cn/stage/recruit/recruit.md"} {
		r := e.get(t, path)
		// noindex as the page, without a canonical, which would contradict it
		if r.status != 200 || r.header.Get("X-Robots-Tag") != "noindex, follow" || r.header.Get("Link") != "" {
			t.Errorf("%s: status %d, X-Robots-Tag %q, Link %q", path, r.status, r.header.Get("X-Robots-Tag"), r.header.Get("Link"))
		}
		if fm, _ := frontMatter(t, r.body); fm["canonical"] != site+strings.TrimSuffix(path, ".md") {
			t.Errorf("%s: canonical %q", path, fm["canonical"])
		}
	}
}

// A view is answered as a view, never redirected to its page nor sent back to the frontend
func TestMarkdownRedirectsToCanonical(t *testing.T) {
	e := newTestEnv(t)
	for path, want := range map[string]string{
		"/en/result/CN/item/30012.md":                 "/en/result/cn/item/30012.md",
		"/en/result/cn/item/30012.md/":                "/en/result/cn/item/30012.md",
		"/en/result/cn/Item/30012.md?utm_source=x":    "/en/result/cn/item/30012.md?utm_source=x",
		"/EN/result/cn/item/30012.md":                 "/en/result/cn/item/30012.md",
		"/en/result/cn/item/%33%30%30%31%32.md":       "/en/result/cn/item/30012.md",
		"/en/result/cn/item/30012%2Emd":               "/en/result/cn/item/30012.md",
		"/en/result/cn/STAGE/main_1/main_01-07.md":    "/en/result/cn/stage/main_1/main_01-07.md",
		"/en/result/cn/stage/wrong/main_01-07.md":     "/en/result/cn/stage/main_1/main_01-07.md",
		"/zh/result/kr/stage/wrong/main_01-07.md?a=1": "/zh/result/kr/stage/main_1/main_01-07.md?a=1",
	} {
		r := e.get(t, path)
		if r.status != 301 || r.header.Get("Location") != want || r.header.Get("Cache-Control") != "public, max-age=300" {
			t.Errorf("%s: %d to %q (Cache-Control %q), want 301 to %q", path, r.status, r.header.Get("Location"), r.header.Get("Cache-Control"), want)
		}
	}

	// without the index.html of the frontend, the page is sent back to the frontend; the view is served
	e.shells.shell, e.shells.err = nil, spashell.ErrUnavailable
	if r := e.get(t, "/ja/result/us/item/30012"); r.status != 302 {
		t.Fatalf("page without a shell: status %d", r.status)
	}
	if r := e.get(t, "/ja/result/us/item/30012.md"); r.status != 200 || r.header.Get("Content-Type") != "text/markdown; charset=utf-8" {
		t.Errorf("view without a shell: status %d, Content-Type %q", r.status, r.header.Get("Content-Type"))
	}

	// with the pages off, the view is not found
	app := fiber.New()
	MountDisabled(app)
	for path, want := range map[string]int{"/zh/result/cn/item/30012": 302, "/zh/result/cn/item/30012.md": 404} {
		resp, err := app.Test(httptestRequest(path), -1)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want || want == 404 && (resp.Header.Get("Location") != "" || resp.Header.Get("Cache-Control") != "no-store") {
			t.Errorf("disabled %s: status %d, Location %q, want %d", path, resp.StatusCode, resp.Header.Get("Location"), want)
		}
	}
}

func TestMarkdownNotModified(t *testing.T) {
	e := newTestEnv(t)
	since := func(v string) map[string]string { return map[string]string{"If-Modified-Since": v} }
	const matrixTime = "Wed, 21 Oct 2026 10:30:00 GMT"
	for _, path := range []string{"/en/result/cn/item/30012.md", "/zh/result/cn/stage/main_1/main_01-07.md", "/en/result/cn/item/quiet.md",
		"/en/result/cn/stage.md", "/en/result/cn/stage.md"} {
		r := e.getWith(t, path, since(matrixTime))
		if r.status != 304 || r.body != "" || r.header.Get("Last-Modified") != matrixTime || r.header.Get("Cache-Control") != "public, max-age=60, s-maxage=300" {
			t.Errorf("%s: status %d, %d bytes, Last-Modified %q", path, r.status, len(r.body), r.header.Get("Last-Modified"))
		}
		if r := e.getWith(t, path, since("Wed, 21 Oct 2026 10:29:59 GMT")); r.status != 200 {
			t.Errorf("%s modified since: status %d", path, r.status)
		}
	}
	if r := e.getWith(t, "/en/result/cn/item/30012.md", since(matrixTime)); r.header.Get("Link") == "" {
		t.Error("304 without the Link of the 200")
	}

	// a frontend release changes the page, not the view, which is not made of the index.html
	s, err := spashell.Parse(e.shells.shell.Raw())
	if err != nil {
		t.Fatal(err)
	}
	s.LastModified = time.Date(2026, 10, 21, 11, 0, 0, 0, time.UTC)
	e.shells.shell = s
	if r := e.getWith(t, "/en/result/cn/item/30012", since(matrixTime)); r.status != 200 {
		t.Errorf("page after a release: status %d", r.status)
	}
	if r := e.getWith(t, "/en/result/cn/item/30012.md", since(matrixTime)); r.status != 304 {
		t.Errorf("view after a release: status %d", r.status)
	}
	// a new catalog changes both
	catalog := testCatalog()
	catalog.ChangedAt = lastModified.Add(2 * time.Hour)
	e.data.catalog = catalog
	if r := e.getWith(t, "/en/result/cn/item/30012.md", since("Wed, 21 Oct 2026 11:00:00 GMT")); r.status != 200 || r.header.Get("Last-Modified") != "Wed, 21 Oct 2026 12:30:00 GMT" {
		t.Errorf("view with a new catalog: status %d, Last-Modified %q", r.status, r.header.Get("Last-Modified"))
	}
}

func TestMarkdownListViews(t *testing.T) {
	e := newTestEnv(t)
	r := e.get(t, "/zh/result/cn/item.md")
	if r.status != 200 || r.header.Get("Content-Type") != "text/markdown; charset=utf-8" ||
		r.header.Get("Cache-Control") != "public, max-age=60, s-maxage=300" || r.header.Get("Last-Modified") != "Wed, 21 Oct 2026 10:30:00 GMT" ||
		r.header.Get("Link") != `<https://penguin-stats.io/zh/result/cn/item>; rel="canonical"` || r.header.Get("X-Robots-Tag") != "" {
		t.Fatalf("item list: status %d, headers %v", r.status, r.header)
	}
	fm, rest := frontMatter(t, r.body)
	for k, want := range map[string]string{
		"title":             "素材掉率 · 按素材（国服）",
		"items":             "3",
		"item_url_template": "https://penguin-stats.io/zh/result/cn/item/{item_id}.md",
		"canonical":         "https://penguin-stats.io/zh/result/cn/item",
		"api":               "https://penguin-stats.io/PenguinStats/api/v2/result/matrix?server=CN&show_closed_zones=true",
		"as_of":             "2026-10-21T10:30:00Z",
	} {
		if fm[k] != want {
			t.Errorf("item list front matter %s: %q, want %q", k, fm[k], want)
		}
	}
	// linked, by category
	assertOnce(t, rest, "## 材料\n\n- [源岩](https://penguin-stats.io/zh/result/cn/item/30011.md) `30011`\n- [固源岩](https://penguin-stats.io/zh/result/cn/item/30012.md) `30012`\n\n## 活动道具")
	golden(t, "testdata/item_list_zh_cn.golden.md", r.body)

	r = e.get(t, "/en/result/cn/stage.md")
	fm, rest = frontMatter(t, r.body)
	if fm["stages"] != "3" || fm["stage_url_template"] != "https://penguin-stats.io/en/result/cn/stage/{zone_id}/{stage_id}.md" {
		t.Errorf("stage list front matter %v", fm)
	}
	// by zone, with the IDs the template takes; the recruitment statistics are no stage of the list
	assertOnce(t, rest, "### Evil Time Part 1 `main_1`\n\n- 1-7 `main_01-07`")
	if strings.Contains(rest, "recruit") {
		t.Error("the stage list lists the recruitment statistics")
	}
	golden(t, "testdata/stage_list_en_cn.golden.md", r.body)

	// a list without the matrix lists what exists on the server, cached for a minute, without as_of
	e.data.matrices = nil
	r = e.get(t, "/en/result/cn/item.md")
	if fm, rest := frontMatter(t, r.body); r.status != 200 || r.header.Get("Cache-Control") != "public, max-age=60" || r.header.Get("Last-Modified") != "" ||
		fm["as_of"] != "" || fm["api"] != "" || !strings.Contains(rest, "/en/result/cn/item/quiet.md") || !strings.Contains(rest, "being calculated") {
		t.Errorf("item list without the matrix: status %d, Cache-Control %q\n%s", r.status, r.header.Get("Cache-Control"), r.body)
	}
}

func TestMarkdownListsAreRenderedOncePerData(t *testing.T) {
	e := newTestEnv(t)
	page, view := e.get(t, "/en/result/cn/item"), e.get(t, "/en/result/cn/item.md")
	// cached apart
	if again := e.get(t, "/en/result/cn/item"); again.body != page.body || !strings.HasPrefix(again.header.Get("Content-Type"), "text/html") {
		t.Error("the list page changed after its view")
	}
	if again := e.get(t, "/en/result/cn/item.md"); again.body != view.body || again.header.Get("Content-Type") != "text/markdown; charset=utf-8" {
		t.Error("the list view changed without the data changing")
	}
	// a frontend release changes the page, not the view
	s, err := spashell.Parse([]byte(strings.Replace(string(e.shells.shell.Raw()), "app.2f494de9.js", "app.00000000.js", 1)))
	if err != nil {
		t.Fatal(err)
	}
	e.shells.shell = s
	if r := e.get(t, "/en/result/cn/item.md"); r.body != view.body {
		t.Error("the list view changed with the shell")
	}
	// the worker refreshed the matrix: evil dropped no more
	e.data.matrices = map[string]*service.SEOMatrix{"CN": service.NewSEOMatrix("CN", []*modelv2.OneDropMatrixElement{
		{StageID: "main_01-07", ItemID: "30012", Times: 10, Quantity: 12, StartTime: 1556676000000},
	}, lastModified.Add(30*time.Minute))}
	if r := e.get(t, "/en/result/cn/item.md"); strings.Contains(r.body, "/item/evil") || r.header.Get("Last-Modified") != "Wed, 21 Oct 2026 11:00:00 GMT" {
		t.Errorf("list view not rendered again for the new matrix (Last-Modified %q)", r.header.Get("Last-Modified"))
	}
}

// The lists stay well under about 100,000 characters (meta#18) with a quarter more stages than CN has (1,586 in
// October 2026, in 167 zones: about 45,000 characters), all named and identified as long as the longest of today
func TestMarkdownListSize(t *testing.T) {
	e := newTestEnv(t)
	var items []*model.Item
	var stages []*model.Stage
	var zones []*model.Zone
	var rows []*modelv2.OneDropMatrixElement
	for z := 0; z < 200; z++ {
		zoneID := fmt.Sprintf("permanent_sidestory_%02d_zone1", z)
		name := fmt.Sprintf("The Knight & The Hunters - Side Story %d", z)
		zones = append(zones, &model.Zone{ArkZoneID: zoneID, ZoneID: z + 1, Index: z, Category: "ACTIVITY_PERMANENT", Name: i18n(name, name, name, name), Existence: existence(all...)})
		for s := 0; s < 10; s++ {
			id := fmt.Sprintf("act%02dside_%02d_perm", z, s)
			code := fmt.Sprintf("DV-EX-%d", s)
			stages = append(stages, &model.Stage{ArkStageID: id, StageID: z*10 + s + 1, ZoneID: z + 1, Code: i18n(code, code, code, code), Existence: existence(all...)})
			rows = append(rows, &modelv2.OneDropMatrixElement{StageID: id, ItemID: fmt.Sprintf("item_%03d", (z*10+s)%300), Times: 10, Quantity: 1, StartTime: 1556676000000})
		}
	}
	for i := 0; i < 300; i++ {
		id := fmt.Sprintf("item_%03d", i)
		name := fmt.Sprintf("Rhodes Island Supplies Crate %d", i)
		items = append(items, &model.Item{ArkItemID: id, Type: "ACTIVITY_ITEM", SortID: i, Name: i18n(name, name, name, name), Existence: existence(all...)})
	}
	e.data.catalog = service.NewSEOCatalog(items, stages, zones)
	e.data.matrices = map[string]*service.SEOMatrix{"CN": service.NewSEOMatrix("CN", rows, lastModified)}
	for _, path := range []string{"/en/result/cn/stage.md", "/en/result/cn/item.md", "/zh/result/cn/stage.md"} {
		r := e.get(t, path)
		fm, _ := frontMatter(t, r.body)
		if n := len([]rune(r.body)); r.status != 200 || n > 80000 || fm["stages"] != "" && fm["stages"] != "2000" {
			t.Errorf("%s: status %d, %d characters, %s stages", path, r.status, n, fm["stages"])
		} else {
			t.Logf("%s: %d characters", path, n)
		}
	}
}

var mdAlternateRe = regexp.MustCompile(`<link rel="alternate" type="text/markdown" href="([^"]*)">`)

// The pages link their views and the guide to the site, in a Link header and in the head
func TestPagesLinkTheirViews(t *testing.T) {
	e := newTestEnv(t)
	links := func(view string) string {
		return `<` + site + view + `>; rel="alternate"; type="text/markdown", <https://penguin-stats.io/llms.txt>; rel="describedby"`
	}
	for _, path := range []string{
		"/en/result/cn/item/30012", "/zh/result/us/stage/main_1/main_01-07", "/ja/result/cn/item", "/ko/result/kr/stage",
		// not indexed, but with a view all the same
		"/en/result/cn/item/quiet", "/en/result/cn/stage/recruit/recruit",
	} {
		r := e.get(t, path)
		if r.status != 200 || r.header.Get("Link") != links(path+".md") {
			t.Errorf("%s: status %d, Link %q", path, r.status, r.header.Get("Link"))
		}
		if m := mdAlternateRe.FindAllStringSubmatch(r.body, -1); len(m) != 1 || m[0][1] != site+path+".md" {
			t.Errorf("%s: Markdown alternates %v", path, m)
		}
		// the view is there
		if v := e.get(t, path+".md"); v.status != 200 || v.header.Get("Content-Type") != "text/markdown; charset=utf-8" {
			t.Errorf("%s.md: status %d", path, v.status)
		}
	}
	// a 304 has the headers of the 200
	if r := e.getWith(t, "/en/result/cn/item/30012", map[string]string{"If-Modified-Since": "Wed, 21 Oct 2026 10:30:00 GMT"}); r.status != 304 || r.header.Get("Link") != links("/en/result/cn/item/30012.md") {
		t.Errorf("304: status %d, Link %q", r.status, r.header.Get("Link"))
	}
	// no view, no link: a 404, the app as is
	for _, path := range []string{"/en/result/cn/item/nope", "/en/result/cn", "/en/result"} {
		if r := e.get(t, path); r.header.Get("Link") != "" || mdAlternateRe.MatchString(r.body) {
			t.Errorf("%s: status %d, Link %q", path, r.status, r.header.Get("Link"))
		}
	}
	// the page of an item whose matrix is being calculated links its view, which is a 503 too
	e.data.matrices = nil
	if r := e.get(t, "/en/result/cn/item/30012"); r.status != 503 || r.header.Get("Link") != links("/en/result/cn/item/30012.md") {
		t.Errorf("cold page: status %d, Link %q", r.status, r.header.Get("Link"))
	}
}

// The index.html of the frontend links /llms.txt in its head (frontend-v2 v3.13.42): the page keeps that link and
// adds none, the Link header has its own, and a Link set before is kept
func TestPagesLinkTheGuideOnce(t *testing.T) {
	e := newTestEnv(t)
	raw := strings.Replace(string(e.shells.shell.Raw()), "</head>",
		`<link rel="api-catalog" href="/.well-known/api-catalog"><link rel="describedby" type="text/plain" href="/llms.txt"></head>`, 1)
	s, err := spashell.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	e.shells.shell = s
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderLink, `</x.css>; rel="preload"; as="style"`)
		return c.Next()
	})
	NewHandler(site, 5*time.Minute, e.shells, e.data).Mount(app)
	resp, err := app.Test(httptestRequest("/en/result/cn/item/30012"), -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if n := strings.Count(string(body), `rel="describedby"`); n != 1 {
		t.Errorf("%d describedby links in the page, want the shell's", n)
	}
	assertOnce(t, string(body), `<link rel="api-catalog" href="/.well-known/api-catalog">`, `<link rel="alternate" type="text/markdown" `)
	if got, want := resp.Header.Get("Link"), `</x.css>; rel="preload"; as="style", <https://penguin-stats.io/en/result/cn/item/30012.md>; rel="alternate"; type="text/markdown", <https://penguin-stats.io/llms.txt>; rel="describedby"`; got != want {
		t.Errorf("Link %q, want %q", got, want)
	}
}

func TestMarkdownCold(t *testing.T) {
	e := newTestEnv(t)
	e.data.matrices = nil
	r := e.get(t, "/en/result/cn/item/30012.md")
	if r.status != 503 || r.header.Get("Retry-After") != "120" || r.header.Get("Cache-Control") != "no-store" ||
		r.header.Get("X-Robots-Tag") != "noindex, follow" || r.header.Get("Link") != "" {
		t.Errorf("matrix not calculated: status %d, Retry-After %q, Cache-Control %q, X-Robots-Tag %q", r.status,
			r.header.Get("Retry-After"), r.header.Get("Cache-Control"), r.header.Get("X-Robots-Tag"))
	}
	if fm, rest := frontMatter(t, r.body); fm["as_of"] != "" || fm["api"] != "" || !strings.Contains(rest, "The drop data is being calculated.") || strings.Contains(rest, "| ") {
		t.Errorf("matrix not calculated: %s", r.body)
	}

	e.data.catalog = nil
	r = e.get(t, "/zh/result/cn/item/30012.md")
	if r.status != 503 || r.header.Get("Retry-After") != "120" || r.header.Get("Cache-Control") != "no-store" ||
		r.header.Get("Content-Type") != "text/markdown; charset=utf-8" || r.body != "掉落数据计算中，请稍后再来。\n" {
		t.Errorf("catalog not loaded: status %d, Retry-After %q, Cache-Control %q, body %q", r.status,
			r.header.Get("Retry-After"), r.header.Get("Cache-Control"), r.body)
	}
}

func TestMarkdownHead(t *testing.T) {
	e := newTestEnv(t)
	r := e.do(t, fiber.MethodHead, "/en/result/cn/item/30012.md")
	if r.status != 200 || r.body != "" || r.header.Get("Content-Type") != "text/markdown; charset=utf-8" {
		t.Errorf("HEAD: %d %q", r.status, r.header.Get("Content-Type"))
	}
}

func TestMarkdownNotInSitemaps(t *testing.T) {
	e := newTestEnv(t)
	warmAll(e)
	for u := range sitemapURLs(t, e) {
		if strings.HasSuffix(u, ".md") {
			t.Errorf("%s in a sitemap", u)
		}
	}
	// a page whose ID ends in .md would be served as the view of another page
	if routable("/en/result/cn/item/a.md", kindItem) || !routable("/en/result/cn/item/a.mdx", kindItem) {
		t.Error("routable: IDs ending in .md")
	}
}

func TestParseRouteMarkdown(t *testing.T) {
	for path, want := range map[string]route{
		"/en/result/cn/item/30012.md":           {kind: kindItem, lang: "en", server: "CN", itemID: "30012", md: true},
		"/en/result/cn/item/a.md.md":            {kind: kindItem, lang: "en", server: "CN", itemID: "a.md", md: true},
		"/en/result/cn/stage/main_1/x.md":       {kind: kindStage, lang: "en", server: "CN", zoneID: "main_1", stageID: "x", md: true},
		"/en/result/cn/item.md":                 {kind: kindItemList, lang: "en", server: "CN", md: true},
		"/en/result/cn.md":                      {kind: kindOther, lang: "en", server: "CN", md: true},
		"/en/result/cn/item/30012.MD":           {kind: kindItem, lang: "en", server: "CN", itemID: "30012.MD"},
		"/en/result/cn/item/.md":                {kind: kindItem, lang: "en", server: "CN", itemID: ".md"},
		"/en/result/cn/item/30012.md/":          {kind: kindItem, lang: "en", server: "CN", itemID: "30012", md: true},
		"/en/result/cn/item/30012.md?not=query": {kind: kindItem, lang: "en", server: "CN", itemID: "30012.md?not=query"},
	} {
		if got := parseRoute(path); got != want {
			t.Errorf("parseRoute(%q) = %+v, want %+v", path, got, want)
		}
	}
	if got := (route{kind: kindItem, lang: "en", server: "CN", itemID: "a/b", md: true}).canonicalPath(); got != "/en/result/cn/item/a%2Fb.md" {
		t.Errorf("canonicalPath %q", got)
	}
}

func httptestRequest(path string) *http.Request {
	return httptest.NewRequest(fiber.MethodGet, path, nil)
}
