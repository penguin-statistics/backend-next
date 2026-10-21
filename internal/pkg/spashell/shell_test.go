package spashell

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

// testdata/index.html is https://penguin-stats.io/ of frontend-v2 v3.13.32, as fetched on 2026-10-21.
func loadIndex(t testing.TB) []byte {
	t.Helper()
	doc, err := os.ReadFile("testdata/index.html")
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func render(s *Shell, lang, head, body string) string {
	var buf bytes.Buffer
	s.Render(&buf, lang, []byte(head), []byte(body))
	return buf.String()
}

func TestParseProductionIndex(t *testing.T) {
	doc := loadIndex(t)
	s, err := Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Spliceable() {
		t.Fatal("the production index.html is not spliceable")
	}
	out := render(s, "en", "<!--HEAD-->", "<!--BODY-->")

	for _, want := range []string{
		`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">`,
		`<!--HEAD--></head><body>`,
		`<span id="peL_footer--year">2021</span></div><!--BODY--></main><script>`,
		// the tags no page sets are kept
		`<meta name="twitter:card" content="summary_large_image">`,
		`<meta name="twitter:image" content="https://penguin-stats.s3.amazonaws.com/images/twitter-card.png">`,
		`<meta property="og:image" content="https://penguin.upyun.galvincdn.com/logos/penguin_stats_logo.png">`,
		`<meta property="og:image:width" content="800">`,
		`<meta name="apple-mobile-web-app-title" content="企鹅物流数据统计">`,
		`<link rel="manifest" href="/manifest.json">`,
		`<script src="/js/chunk-vendors.e6bcee3d.js"></script><script src="/js/app.2f494de9.js"></script></body></html>`,
	} {
		if strings.Count(out, want) != 1 {
			t.Errorf("want %q once in the output", want)
		}
	}
	for _, gone := range []string{
		`<title>`, `name="description"`, `name="robots"`, `name="googlebot"`, `property="og:url"`, `property="og:type"`,
		`property="og:title"`, `property="og:description"`, `property="og:image:alt"`, `name="twitter:image:alt"`, `lang="zh"`,
	} {
		if strings.Contains(out, gone) {
			t.Errorf("%q is still in the output", gone)
		}
	}

	// nothing else changed: the output is the document without the replaced tags, plus the markup spliced in
	stripped := string(doc)
	for _, re := range []string{
		`<title>[^<]*</title>`,
		`<meta name="(description|robots|googlebot|twitter:image:alt)" content="[^"]*">`,
		`<meta property="og:(url|type|title|description|image:alt)" content="[^"]*">`,
	} {
		stripped = regexp.MustCompile(re).ReplaceAllString(stripped, "")
	}
	stripped = strings.Replace(stripped, `<html lang="zh">`, `<html lang="en">`, 1)
	stripped = strings.Replace(stripped, `</head>`, `<!--HEAD--></head>`, 1)
	stripped = strings.Replace(stripped, `</main>`, `<!--BODY--></main>`, 1)
	if out != stripped {
		t.Error("the output differs from the document in more than the replaced tags and the markup spliced in")
	}
}

func TestParseVariants(t *testing.T) {
	doc := `<!doctype html><HTML class=x LANG='zh-CN' data-y="1"><head>` +
		`<title data-x>旧</title><meta content="d" name="description"/><meta name=robots content=index>` +
		`<meta property=og:locale content=zh_CN><meta property="og:locale:alternate" content="en_US">` +
		`<meta property="og:image" content="keep"><link rel="canonical" href="https://x/"><link rel=alternate hreflang=en href=/en>` +
		`<script type="application/ld+json">{"a":1}</script></HEAD><body><main class="m" id='app'><div>loader</div></MAIN></body></html>`
	s, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Spliceable() {
		t.Fatal("not spliceable")
	}
	got := render(s, `ja"`, "[H]", "[B]")
	want := `<!doctype html><html class=x data-y="1" lang="ja&#34;"><head><meta property="og:image" content="keep">[H]</HEAD>` +
		`<body><main class="m" id='app'><div>loader</div>[B]</MAIN></body></html>`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestParseUnspliceable(t *testing.T) {
	for name, doc := range map[string]string{
		"no </main>":        `<html><head></head><body><main id="app"></body></html>`,
		"no </head>":        `<html><head><body><main id="app"></main></body></html>`,
		"mount not a main":  `<html><head></head><body><div id="app"></div></body></html>`,
		"mount in the head": `<html><head><main id="app"></main></head><body></body></html>`,
	} {
		s, err := Parse([]byte(doc))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s.Spliceable() {
			t.Errorf("%s: spliceable", name)
		}
		if got := render(s, "en", "[H]", "[B]"); got != doc {
			t.Errorf("%s: rendered %q, want the document as is", name, got)
		}
	}
}

func TestParseNotShell(t *testing.T) {
	for _, doc := range []string{
		`<html><head><title>Just a moment...</title></head><body><div id="challenge"></div></body></html>`,
		`{"code":"NOT_FOUND"}`,
		``,
	} {
		if _, err := Parse([]byte(doc)); err != ErrNotShell {
			t.Errorf("Parse(%q): %v, want ErrNotShell", doc, err)
		}
	}
}
