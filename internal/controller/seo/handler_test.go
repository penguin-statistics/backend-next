package seo

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/helmet/v2"
	"go.uber.org/fx/fxtest"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/spashell"
	"exusiai.dev/backend-next/internal/service"
)

var update = flag.Bool("update", false, "update the golden files")

const (
	site    = "https://penguin-stats.io"
	hostile = `</script><img src=x onerror=alert(1)>"'&`
)

var lastModified = time.Date(2026, 10, 21, 10, 30, 0, 0, time.UTC)

type fakeData struct {
	catalog  *service.SEOCatalog
	matrices map[string]*service.SEOMatrix
}

func (d *fakeData) Catalog() *service.SEOCatalog            { return d.catalog }
func (d *fakeData) Matrix(server string) *service.SEOMatrix { return d.matrices[server] }

type fakeShells struct {
	shell *spashell.Shell
	err   error
}

func (s *fakeShells) Get(context.Context) (*spashell.Shell, error) { return s.shell, s.err }

func i18n(zh, en, ja, ko string) []byte {
	b, _ := json.Marshal(map[string]string{"zh": zh, "en": en, "ja": ja, "ko": ko})
	return b
}

func existence(servers ...string) []byte {
	m := map[string]map[string]any{}
	for _, s := range []string{"CN", "US", "JP", "KR"} {
		m[s] = map[string]any{"exist": false}
	}
	for _, s := range servers {
		m[s] = map[string]any{"exist": true, "openTime": 1556676000000}
	}
	b, _ := json.Marshal(m)
	return b
}

var all = []string{"CN", "US", "JP", "KR"}

func testCatalog() *service.SEOCatalog {
	return service.NewSEOCatalog(
		[]*model.Item{
			{ArkItemID: "30011", Type: "MATERIAL", SortID: 1, Name: i18n("源岩", "Orirock", "源岩", "원암"), Existence: existence(all...), Group: null.StringFrom("orirock")},
			{ArkItemID: "30012", Type: "MATERIAL", SortID: 2, Name: i18n("固源岩", "Orirock Cube", "初級源岩", "원암 큐브"), Existence: existence(all...), Group: null.StringFrom("orirock")},
			{ArkItemID: "30013", Type: "MATERIAL", SortID: 3, Name: i18n("固源岩组", "Orirock Cluster", "", ""), Existence: existence("CN"), Group: null.StringFrom("orirock")},
			{ArkItemID: "evil", Type: "ACTIVITY_ITEM", SortID: 4, Name: i18n(hostile, hostile, hostile, hostile), Existence: existence("CN")},
			{ArkItemID: "quiet", Type: "MATERIAL", SortID: 5, Name: i18n("无掉落", "No Drops", "", ""), Existence: existence("CN")},
			{ArkItemID: "tag_1", Type: "RECRUIT_TAG", SortID: 6, Name: i18n("近卫干员", "Guard", "", ""), Existence: existence(all...)},
		},
		[]*model.Stage{
			{ArkStageID: "main_01-07", StageID: 1, ZoneID: 1, Code: i18n("1-7", "1-7", "1-7", "1-7"), Sanity: null.IntFrom(6), Existence: existence(all...)},
			{ArkStageID: "act1_01", StageID: 2, ZoneID: 2, Code: i18n(hostile, hostile, hostile, hostile), Sanity: null.IntFrom(15), Existence: existence("CN")},
			{ArkStageID: "act1_02", StageID: 3, ZoneID: 2, Code: i18n("EV-2", "EV-2", "EV-2", "EV-2"), Sanity: null.IntFrom(15), Existence: existence("CN")},
			{ArkStageID: "recruit", StageID: 4, ZoneID: 3, Code: i18n("公开招募", "Recruit", "公開求人", "공개모집"), Existence: existence(all...)},
		},
		[]*model.Zone{
			{ArkZoneID: "main_1", ZoneID: 1, Index: 1, Category: "MAINLINE", Name: i18n("黑暗时代·上", "Evil Time Part 1", "暗黒時代・上", "암흑시대・상"), Existence: existence(all...)},
			{ArkZoneID: "act1_zone1", ZoneID: 2, Index: 0, Category: "ACTIVITY", Name: i18n(hostile, hostile, hostile, hostile), Existence: existence("CN")},
			{ArkZoneID: "recruit", ZoneID: 3, Category: "RECRUIT", Name: i18n("公开招募", "Recruit", "", ""), Existence: existence(all...)},
		},
	)
}

func testMatrices() map[string]*service.SEOMatrix {
	return map[string]*service.SEOMatrix{
		"CN": service.NewSEOMatrix("CN", []*modelv2.OneDropMatrixElement{
			{StageID: "main_01-07", ItemID: "30012", Times: 1061347, Quantity: 1322056, StartTime: 1556676000000},
			{StageID: "act1_01", ItemID: "30012", Times: 5778, Quantity: 13159, StartTime: 1688025600000, EndTime: null.IntFrom(1689105600000)},
			{StageID: "act1_02", ItemID: "30012", Times: 100, Quantity: 300, StartTime: 1688025600000, EndTime: null.IntFrom(1689105600000)},
			{StageID: "main_01-07", ItemID: "30011", Times: 1061347, Quantity: 0, StartTime: 1556676000000},
			{StageID: "act1_01", ItemID: "evil", Times: 5778, Quantity: 5778, StartTime: 1688025600000, EndTime: null.IntFrom(1689105600000)},
			{StageID: "recruit", ItemID: "tag_1", Times: 50, Quantity: 10, StartTime: 1556676000000},
		}, lastModified),
		"US": service.NewSEOMatrix("US", []*modelv2.OneDropMatrixElement{
			{StageID: "main_01-07", ItemID: "30012", Times: 2000, Quantity: 2500, StartTime: 1579150800000},
		}, lastModified),
	}
}

func loadShell(t testing.TB) *spashell.Shell {
	t.Helper()
	doc, err := os.ReadFile("../../pkg/spashell/testdata/index.html")
	if err != nil {
		t.Fatal(err)
	}
	s, err := spashell.Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	s.LastModified = time.Date(2026, 10, 21, 9, 9, 28, 0, time.UTC)
	return s
}

type testEnv struct {
	app    *fiber.App
	data   *fakeData
	shells *fakeShells
}

// newTestEnv mounts the pages behind the helmet middleware of the service app (internal/server/httpserver)
func newTestEnv(t testing.TB) *testEnv {
	e := &testEnv{
		data:   &fakeData{catalog: testCatalog(), matrices: testMatrices()},
		shells: &fakeShells{shell: loadShell(t)},
	}
	e.app = fiber.New(fiber.Config{Immutable: true})
	e.app.Use(helmet.New(helmet.Config{
		HSTSMaxAge:         31356000,
		HSTSPreloadEnabled: true,
		ReferrerPolicy:     "strict-origin-when-cross-origin",
		PermissionPolicy:   "interest-cohort=()",
	}))
	NewHandler(site+"/", 5*time.Minute, e.shells, e.data).Mount(e.app)
	return e
}

type response struct {
	status int
	header http.Header
	body   string
}

func (e *testEnv) get(t testing.TB, path string) response {
	t.Helper()
	return e.do(t, fiber.MethodGet, path)
}

func (e *testEnv) do(t testing.TB, method, path string) response {
	t.Helper()
	resp, err := e.app.Test(httptest.NewRequest(method, path, nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: string(b)}
}

var (
	canonicalRe = regexp.MustCompile(`<link rel="canonical" href="([^"]*)">`)
	hreflangRe  = regexp.MustCompile(`<link rel="alternate" hreflang="([^"]*)" href="([^"]*)">`)
	jsonLDRe    = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
	ssrRe       = regexp.MustCompile(`(?s)<section id="pe-ssr".*?</section>`)
)

func jsonLD(t *testing.T, body string) map[string]any {
	t.Helper()
	m := jsonLDRe.FindAllStringSubmatch(body, -1)
	if len(m) != 1 {
		t.Fatalf("%d JSON-LD scripts, want 1", len(m))
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(m[0][1]), &v); err != nil {
		t.Fatalf("JSON-LD does not parse: %v\n%s", err, m[0][1])
	}
	return v
}

func graphNode(t *testing.T, ld map[string]any, typ string) map[string]any {
	t.Helper()
	for _, n := range ld["@graph"].([]any) {
		if m := n.(map[string]any); m["@type"] == typ {
			return m
		}
	}
	t.Fatalf("no %s in the JSON-LD", typ)
	return nil
}

// assertApp checks that the response is still the working app: its scripts and mount element are there
func assertApp(t *testing.T, r response) {
	t.Helper()
	for _, want := range []string{`<main id="app">`, `<script src="/js/app.2f494de9.js"></script></body></html>`, `peL_footer--year`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("the app is broken: no %q", want)
		}
	}
}

func assertOnce(t *testing.T, body string, tags ...string) {
	t.Helper()
	for _, tag := range tags {
		if n := strings.Count(body, tag); n != 1 {
			t.Errorf("%q %d times, want once", tag, n)
		}
	}
}

func TestItemPage(t *testing.T) {
	e := newTestEnv(t)
	r := e.get(t, "/zh/result/cn/item/30012")
	if r.status != 200 {
		t.Fatalf("status %d", r.status)
	}
	for k, want := range map[string]string{
		"Content-Type":     "text/html; charset=utf-8",
		"Content-Language": "zh",
		"Cache-Control":    "public, max-age=300",
		"Last-Modified":    "Wed, 21 Oct 2026 10:30:00 GMT",
		"X-Robots-Tag":     "",
		// helmet's isolation would block the app's cross-origin images and scripts
		"Cross-Origin-Embedder-Policy": "",
		"Cross-Origin-Opener-Policy":   "",
		"Cross-Origin-Resource-Policy": "",
		"Origin-Agent-Cluster":         "",
	} {
		if got := r.header.Get(k); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
	}
	assertApp(t, r)
	assertOnce(t, r.body,
		`<html lang="zh">`,
		`<title>固源岩 掉率统计（国服） | 企鹅物流数据统计</title>`,
		`<meta name="robots" content="index,follow">`,
		`<link rel="canonical" href="https://penguin-stats.io/zh/result/cn/item/30012">`,
		`<meta property="og:url" content="https://penguin-stats.io/zh/result/cn/item/30012">`,
		`<meta name="description" `, `<meta property="og:title" `, `<meta property="og:description" `, `<meta property="og:type" `,
		`<meta name="twitter:image:alt" `, `<meta property="og:image:alt" `, `<meta name="googlebot" `,
		`<section id="pe-ssr" lang="zh">`,
	)
	if !strings.Contains(r.body, `<meta name="description" content="固源岩（国服）掉落统计：1-7 百分比 124.56%（样本数 1,061,347）、`) {
		t.Errorf("description: %s", regexp.MustCompile(`<meta name="description"[^>]*>`).FindString(r.body))
	}
	// the table goes at the end of the mount element, after the loader of the app, which the app replaces
	if i, j, k := strings.Index(r.body, `peL_footer--year`), strings.Index(r.body, `<section id="pe-ssr"`), strings.Index(r.body, `</main>`); !(i < j && j < k) {
		t.Error("the table is not at the end of the mount element")
	}

	ld := jsonLD(t, r.body)
	ds := graphNode(t, ld, "Dataset")
	for k, want := range map[string]any{
		"@id":                 "https://penguin-stats.io/zh/result/cn/item/30012#dataset",
		"url":                 "https://penguin-stats.io/zh/result/cn/item/30012",
		"name":                "固源岩 掉落统计（国服）",
		"license":             "https://creativecommons.org/licenses/by-nc/4.0/",
		"dateModified":        "2026-10-21T10:30:00Z",
		"temporalCoverage":    "2019-05-01T02:00:00Z/..",
		"inLanguage":          "zh",
		"isAccessibleForFree": true,
	} {
		if ds[k] != want {
			t.Errorf("Dataset %s: %v, want %v", k, ds[k], want)
		}
	}
	if d := ds["description"].(string); len([]rune(d)) < 50 {
		t.Errorf("Dataset description shorter than 50 characters: %q", d)
	}
	dist := ds["distribution"].([]any)[0].(map[string]any)
	if dist["contentUrl"] != "https://penguin-stats.io/PenguinStats/api/v2/result/matrix?server=CN&itemFilter=30012&show_closed_zones=true" {
		t.Errorf("distribution %v", dist)
	}
	crumbs := graphNode(t, ld, "BreadcrumbList")["itemListElement"].([]any)
	if len(crumbs) != 3 || crumbs[1].(map[string]any)["item"] != "https://penguin-stats.io/zh/result/cn/item" || crumbs[2].(map[string]any)["item"] != nil {
		t.Errorf("breadcrumbs %v", crumbs)
	}

	// rows in the frontend's order: rate, then times
	var stages []string
	for _, m := range regexp.MustCompile(`<tr><th scope="row"><a href="[^"]*/stage/[^/]*/([^"]*)">`).FindAllStringSubmatch(r.body, -1) {
		stages = append(stages, m[1])
	}
	if strings.Join(stages, ",") != "act1_02,act1_01,main_01-07" {
		t.Errorf("rows %v", stages)
	}

	golden(t, "testdata/item_zh_cn.golden.html", markup(r.body))
}

// markup is what the page added to the shell
func markup(body string) string {
	head := body[strings.Index(body, "<title>"):strings.Index(body, "</head>")]
	return head + "\n" + ssrRe.FindString(body) + "\n"
}

func golden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("markup differs from %s (go test -update to update):\n%s", path, got)
	}
}

func TestStagePage(t *testing.T) {
	e := newTestEnv(t)
	r := e.get(t, "/en/result/cn/stage/main_1/main_01-07")
	if r.status != 200 {
		t.Fatalf("status %d", r.status)
	}
	assertApp(t, r)
	assertOnce(t, r.body,
		`<html lang="en">`,
		`<title>1-7 Evil Time Part 1 Drop Rates (CN server) | Penguin Statistics</title>`,
		`<link rel="canonical" href="https://penguin-stats.io/en/result/cn/stage/main_1/main_01-07">`,
		`<h2>Evil Time Part 1</h2>`, `<h1>Statistics of 1-7</h1>`, `<p>Sanity: 6</p>`,
		// links the items, with the expected sanity per item
		`<tr><th scope="row"><a href="https://penguin-stats.io/en/result/cn/item/30012">Orirock Cube</a></th><td>1322056</td><td>1061347</td><td>124.56%</td><td>4.82</td>`,
		// an item never dropped has no expected sanity
		`<td>0</td><td>1061347</td><td>0.00%</td><td>—</td>`,
	)
	if !strings.Contains(r.body, `<td><time datetime="2019-05-01T10:00:00&#43;08:00">2019-05-01</time> until now</td>`) {
		t.Error("no open time range")
	}
	golden(t, "testdata/stage_en_cn.golden.html", markup(r.body))
	ds := graphNode(t, jsonLD(t, r.body), "Dataset")
	if ds["distribution"].([]any)[0].(map[string]any)["contentUrl"] != "https://penguin-stats.io/PenguinStats/api/v2/result/matrix?server=CN&stageFilter=main_01-07&show_closed_zones=true" {
		t.Errorf("distribution %v", ds["distribution"])
	}

	// a closed time range in the words of each language
	for lang, want := range map[string]string{
		"zh": `<time datetime="2023-06-29T16:00:00&#43;08:00">2023-06-29</time> ~ <time datetime="2023-07-12T04:00:00&#43;08:00">2023-07-12</time>`,
		"ko": `<time datetime="2023-06-29T16:00:00&#43;08:00">2023-06-29</time> - <time datetime="2023-07-12T04:00:00&#43;08:00">2023-07-12</time>`,
	} {
		r := e.get(t, "/"+lang+"/result/cn/stage/act1_zone1/act1_02")
		if !strings.Contains(r.body, want) {
			t.Errorf("%s: no %s", lang, want)
		}
	}
}

func TestCanonicalAndHreflang(t *testing.T) {
	e := newTestEnv(t)
	paths := map[string]string{
		"item":       "/item/30012",
		"item_list":  "/item",
		"stage":      "/stage/main_1/main_01-07",
		"stage_list": "/stage",
	}
	for _, lang := range langs {
		for _, server := range []string{"cn", "us"} {
			for kind, suffix := range paths {
				path := "/" + lang + "/result/" + server + suffix
				r := e.get(t, path)
				if r.status != 200 {
					t.Errorf("%s: status %d", path, r.status)
					continue
				}
				if m := canonicalRe.FindAllStringSubmatch(r.body, -1); len(m) != 1 || m[0][1] != site+path {
					t.Errorf("%s: canonical %v", path, m)
				}
				alts := hreflangRe.FindAllStringSubmatch(r.body, -1)
				want := map[string]string{"x-default": site + "/en/result/" + server + suffix}
				for _, l := range langs {
					want[l] = site + "/" + l + "/result/" + server + suffix
				}
				got := map[string]string{}
				for _, a := range alts {
					got[a[1]] = a[2]
				}
				if len(alts) != 5 || len(got) != 5 {
					t.Errorf("%s (%s): hreflang %v", path, kind, alts)
				}
				for l, href := range want {
					if got[l] != href {
						t.Errorf("%s: hreflang %s = %q, want %q", path, l, got[l], href)
					}
				}
				if !strings.Contains(r.body, `<html lang="`+lang+`">`) {
					t.Errorf("%s: html lang", path)
				}
				jsonLD(t, r.body)
			}
		}
	}
}

func TestHostileNamesAreEscaped(t *testing.T) {
	e := newTestEnv(t)
	for _, path := range []string{
		"/en/result/cn/item/evil",
		"/en/result/cn/stage/act1_zone1/act1_01",
		"/ja/result/cn/item/30012",
		"/zh/result/cn/stage",
		"/ko/result/cn/item",
	} {
		r := e.get(t, path)
		if r.status != 200 {
			t.Errorf("%s: status %d", path, r.status)
			continue
		}
		if strings.Contains(r.body, "<img src=x") || strings.Contains(r.body, "onerror=alert(1)>") {
			t.Errorf("%s: the hostile name is not escaped", path)
		}
		// nothing closes a script but the scripts of the shell and the JSON-LD
		if n, want := strings.Count(r.body, "</script>"), strings.Count(string(e.shells.shell.Raw()), "</script>")+1; n != want {
			t.Errorf("%s: %d </script>, want %d", path, n, want)
		}
		if !strings.Contains(r.body, `&lt;/script&gt;&lt;img src=x onerror=alert(1)&gt;&#34;&#39;&amp;`) {
			t.Errorf("%s: the hostile name is not shown escaped", path)
		}
		ld := jsonLD(t, r.body)
		if raw := jsonLDRe.FindStringSubmatch(r.body)[1]; strings.ContainsAny(raw, "<>") {
			t.Errorf("%s: < or > in the JSON-LD", path)
		}
		if strings.HasSuffix(path, "/item/evil") {
			if name := graphNode(t, ld, "Dataset")["name"]; name != hostile+" drop statistics (CN server)" {
				t.Errorf("%s: JSON-LD name %q", path, name)
			}
		}
	}
}

func TestNotFound(t *testing.T) {
	e := newTestEnv(t)
	for _, path := range []string{
		"/zh/result/cn/item/nope",
		"/zh/result/cn/item/tag_1", // not an item the frontend lists
		"/zh/result/us/item/30013", // CN only
		"/zh/result/us/item/evil",  // CN only, and drops on CN only
		"/en/result/cn/stage/main_1/nope",
		"/en/result/kr/stage/act1_zone1/act1_01", // CN only
		"/en/result/cn/foo",
		"/en/result/cn/item/30012/more",
		"/en/result/xx/item",
		"/en/result/xx",
	} {
		r := e.get(t, path)
		if r.status != 404 {
			t.Errorf("%s: status %d, want 404", path, r.status)
			continue
		}
		if r.header.Get("X-Robots-Tag") != "noindex" || r.header.Get("Cache-Control") != "public, max-age=60" {
			t.Errorf("%s: X-Robots-Tag %q, Cache-Control %q", path, r.header.Get("X-Robots-Tag"), r.header.Get("Cache-Control"))
		}
		assertApp(t, r)
		assertOnce(t, r.body, `<meta name="robots" content="noindex">`, `<title>`)
		for _, absent := range []string{`rel="canonical"`, `hreflang=`, `application/ld+json`, `property="og:url"`} {
			if strings.Contains(r.body, absent) {
				t.Errorf("%s: %s on a 404", path, absent)
			}
		}
	}
	r := e.get(t, "/ja/result/cn/stage/main_1/nope")
	assertOnce(t, r.body, `<title>ページが見つかりません | ペンギン急便データ統計処理部門</title>`,
		`<p><a href="https://penguin-stats.io/ja/result/cn/stage">作戦別（大陸版）</a></p>`)
}

func TestNoindexPages(t *testing.T) {
	e := newTestEnv(t)
	for path, robots := range map[string]string{
		// exists, but never dropped
		"/en/result/cn/item/quiet": "noindex, follow",
		// the recruitment statistics have measures of their own
		"/en/result/cn/stage/recruit/recruit": "noindex, follow",
	} {
		r := e.get(t, path)
		if r.status != 200 || r.header.Get("X-Robots-Tag") != robots {
			t.Errorf("%s: status %d, X-Robots-Tag %q", path, r.status, r.header.Get("X-Robots-Tag"))
		}
		assertOnce(t, r.body, `<meta name="robots" content="noindex,follow">`)
		if strings.Contains(path, "recruit") {
			assertOnce(t, r.body, `<title>Recruit Drop Rates (CN server) | Penguin Statistics</title>`)
		}
		for _, absent := range []string{`rel="canonical"`, `hreflang=`, `application/ld+json`} {
			if strings.Contains(r.body, absent) {
				t.Errorf("%s: %s on a page not indexed", path, absent)
			}
		}
	}
	// paths the frontend redirects to its home page are the app as is
	for _, path := range []string{"/zh/result", "/zh/result/", "/zh/result/cn", "/ko/result/KR/"} {
		r := e.get(t, path)
		if r.status != 200 || r.header.Get("X-Robots-Tag") != "noindex" || r.body != string(e.shells.shell.Raw()) {
			t.Errorf("%s: status %d, X-Robots-Tag %q, %d bytes", path, r.status, r.header.Get("X-Robots-Tag"), len(r.body))
		}
	}
}

func TestRedirectsToCanonical(t *testing.T) {
	e := newTestEnv(t)
	for path, want := range map[string]string{
		"/zh/result/CN/item/30012":              "/zh/result/cn/item/30012",
		"/zh/result/cn/item/30012/":             "/zh/result/cn/item/30012",
		"/ZH/result/Cn/Item?utm_source=x":       "/zh/result/cn/item?utm_source=x",
		"/en/result/cn/stage/wrong/main_01-07":  "/en/result/cn/stage/main_1/main_01-07",
		"/en/result/cn/STAGE/main_1/main_01-07": "/en/result/cn/stage/main_1/main_01-07",
		// IDs are case-sensitive: kept as they are, and then not found
		"/en/result/CN/item/ABC": "/en/result/cn/item/ABC",
		// escaped needlessly
		"/en/result/cn/item/%33%30%30%31%32":      "/en/result/cn/item/30012",
		"/en/result/cn/stage/main_1/main%5F01-07": "/en/result/cn/stage/main_1/main_01-07",
		"/en/result/cn/item/a%2fb":                "/en/result/cn/item/a%2Fb",
	} {
		r := e.get(t, path)
		if r.status != 301 || r.header.Get("Location") != want {
			t.Errorf("%s: %d to %q, want 301 to %q", path, r.status, r.header.Get("Location"), want)
		}
	}
	// the canonical paths are served, not redirected again
	for _, path := range []string{"/en/result/cn/item/ABC", "/en/result/cn/item/a%2Fb", "/en/result/cn/stage/main_1/x%20y"} {
		if r := e.get(t, path); r.status != 404 {
			t.Errorf("%s: status %d, want 404", path, r.status)
		}
	}
	if r := e.get(t, "/en/result/cn/item/30012?utm_source=x"); r.status != 200 {
		t.Errorf("with a query: status %d", r.status)
	}
	if r := parseRoute("/en/result/cn/item/%zz"); r.kind != kindNone {
		t.Errorf("malformed escape parsed as %+v", r)
	}
}

func TestListPages(t *testing.T) {
	e := newTestEnv(t)
	r := e.get(t, "/zh/result/cn/item")
	if r.status != 200 || r.header.Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("item list: %d %q", r.status, r.header.Get("Cache-Control"))
	}
	// the items with drops on the server, by category
	links := regexp.MustCompile(`<li><a href="https://penguin-stats\.io/zh/result/cn/item/([^"]+)">`).FindAllStringSubmatch(ssrRe.FindString(r.body), -1)
	var ids []string
	for _, l := range links {
		ids = append(ids, l[1])
	}
	if strings.Join(ids, ",") != "30011,30012,evil" {
		t.Errorf("item list links %v", ids)
	}
	assertOnce(t, r.body, `<h2>材料</h2>`, `<h2>活动道具</h2>`, `<title>素材掉率 · 按素材（国服） | 企鹅物流数据统计</title>`,
		`<meta name="description" content="明日方舟各物品在国服的掉率统计：共 3 种物品、3 个作战。数据更新于 2026-10-21。企鹅物流数据统计——玩家众包的明日方舟掉率数据。">`)
	crumbs := graphNode(t, jsonLD(t, r.body), "BreadcrumbList")["itemListElement"].([]any)
	if len(crumbs) != 2 {
		t.Errorf("breadcrumbs %v", crumbs)
	}

	r = e.get(t, "/en/result/cn/stage")
	golden(t, "testdata/stage_list_en_cn.golden.html", markup(r.body))
	if strings.Contains(r.body, "/stage/recruit/recruit") {
		t.Error("the stage list links the recruitment statistics")
	}

	r = e.get(t, "/ko/result/us/stage")
	stages := regexp.MustCompile(`/ko/result/us/stage/[^/]+/([^"]+)"`).FindAllStringSubmatch(ssrRe.FindString(r.body), -1)
	if len(stages) != 1 || stages[0][1] != "main_01-07" {
		t.Errorf("US stage list %v", stages)
	}
}

func TestListPagesAreRenderedOncePerData(t *testing.T) {
	e := newTestEnv(t)
	first := e.get(t, "/en/result/cn/item")
	if again := e.get(t, "/en/result/cn/item"); again.body != first.body || again.header.Get("Cache-Control") != "public, max-age=300" {
		t.Error("the list page changed without the data changing")
	}
	if !strings.Contains(first.body, "/en/result/cn/item/evil") {
		t.Fatal("item list without evil")
	}

	// the worker refreshed the matrix: evil dropped no more
	e.data.matrices = map[string]*service.SEOMatrix{"CN": service.NewSEOMatrix("CN", []*modelv2.OneDropMatrixElement{
		{StageID: "main_01-07", ItemID: "30012", Times: 10, Quantity: 12, StartTime: 1556676000000},
	}, lastModified.Add(30*time.Minute))}
	r := e.get(t, "/en/result/cn/item")
	if strings.Contains(r.body, "/en/result/cn/item/evil") || r.header.Get("Last-Modified") != "Wed, 21 Oct 2026 11:00:00 GMT" {
		t.Errorf("list page not rendered again for the new matrix (Last-Modified %q)", r.header.Get("Last-Modified"))
	}

	// a frontend release
	s, err := spashell.Parse([]byte(strings.Replace(string(e.shells.shell.Raw()), "app.2f494de9.js", "app.00000000.js", 1)))
	if err != nil {
		t.Fatal(err)
	}
	e.shells.shell = s
	if r := e.get(t, "/en/result/cn/item"); !strings.Contains(r.body, "app.00000000.js") {
		t.Error("list page not rendered again for the new shell")
	}
}

func TestMatrixNotCalculated(t *testing.T) {
	e := newTestEnv(t)
	e.data.matrices = nil
	r := e.get(t, "/en/result/cn/item/30012")
	if r.status != 200 || r.header.Get("Cache-Control") != "public, max-age=60" || r.header.Get("X-Robots-Tag") != "" {
		t.Errorf("status %d, Cache-Control %q, X-Robots-Tag %q", r.status, r.header.Get("Cache-Control"), r.header.Get("X-Robots-Tag"))
	}
	assertOnce(t, r.body, `<link rel="canonical" href="https://penguin-stats.io/en/result/cn/item/30012">`,
		`<p>The drop data is being calculated. Please check back later.</p>`)
	if strings.Contains(r.body, "<table>") || strings.Contains(r.body, "application/ld+json") {
		t.Error("a table or a Dataset without data")
	}
	// the lists link what exists on the server
	r = e.get(t, "/en/result/cn/item")
	if !strings.Contains(r.body, "/en/result/cn/item/quiet") || strings.Contains(r.body, "/en/result/cn/item/tag_1") {
		t.Error("item list without the matrix")
	}
}

func TestCatalogNotLoaded(t *testing.T) {
	e := newTestEnv(t)
	e.data.catalog = nil
	r := e.get(t, "/en/result/cn/item/30012")
	if r.status != 200 || r.header.Get("Cache-Control") != "no-store" || r.body != string(e.shells.shell.Raw()) {
		t.Errorf("status %d, Cache-Control %q", r.status, r.header.Get("Cache-Control"))
	}
}

func TestShellUnavailable(t *testing.T) {
	e := newTestEnv(t)
	e.shells.shell, e.shells.err = nil, spashell.ErrUnavailable
	// the frontend serves the path with the language in upper case, which the ingress does not route here
	r := e.get(t, "/ja/result/kr/item/30012?x=1")
	if r.status != 302 || r.header.Get("Location") != "/JA/result/kr/item/30012?x=1" || r.header.Get("Cache-Control") != "no-store" {
		t.Errorf("status %d to %q, Cache-Control %q", r.status, r.header.Get("Location"), r.header.Get("Cache-Control"))
	}
	r = e.get(t, "/ja/result/kr/item/a%2Fb")
	if r.status != 302 || r.header.Get("Location") != "/JA/result/kr/item/a%2Fb" {
		t.Errorf("status %d to %q", r.status, r.header.Get("Location"))
	}
	r = e.get(t, "/ja/result")
	if r.status != 302 || r.header.Get("Location") != "/JA/result" {
		t.Errorf("status %d to %q", r.status, r.header.Get("Location"))
	}
	// not routed by the ingress: no redirect loop
	r = e.get(t, "/JA/result/kr/item/30012")
	if r.status != 503 || r.header.Get("Retry-After") == "" || r.header.Get("Cache-Control") != "no-store" {
		t.Errorf("status %d, Retry-After %q", r.status, r.header.Get("Retry-After"))
	}
}

func TestUnspliceableShellIsServedAsIs(t *testing.T) {
	e := newTestEnv(t)
	s, err := spashell.Parse([]byte(`<html><head><title>x</title></head><body><div id="app"></div></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	e.shells.shell = s
	r := e.get(t, "/en/result/cn/item/30012")
	if r.status != 200 || r.body != string(s.Raw()) {
		t.Errorf("status %d, body %q", r.status, r.body)
	}
}

func TestHead(t *testing.T) {
	e := newTestEnv(t)
	r := e.do(t, fiber.MethodHead, "/en/result/cn/item/30012")
	if r.status != 200 || r.body != "" || r.header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("HEAD: %d %q", r.status, r.header.Get("Content-Type"))
	}
}

func TestSameHTMLForEveryone(t *testing.T) {
	e := newTestEnv(t)
	var first []byte
	for _, h := range []map[string]string{
		{},
		{"User-Agent": "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", "Accept-Language": "ja"},
		{"User-Agent": "Mozilla/5.0 (iPhone)", "Accept-Language": "ko-KR", "Cookie": "a=b"},
	} {
		req := httptest.NewRequest(fiber.MethodGet, "/en/result/cn/item/30012", nil)
		for k, v := range h {
			req.Header.Set(k, v)
		}
		resp, err := e.app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if first == nil {
			first = b
		} else if !bytes.Equal(first, b) {
			t.Errorf("different HTML for %v", h)
		}
		if v := resp.Header.Get("Vary"); v != "" {
			t.Errorf("Vary %q", v)
		}
	}
}

func TestItemPageCapsRows(t *testing.T) {
	e := newTestEnv(t)
	var rows []*modelv2.OneDropMatrixElement
	var stages []*model.Stage
	for i := 0; i < 120; i++ {
		id := "s" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + string(rune('a'+i/26))
		stages = append(stages, &model.Stage{ArkStageID: id, StageID: 100 + i, ZoneID: 1, Code: i18n(id, id, id, id), Existence: existence("CN")})
		rows = append(rows, &modelv2.OneDropMatrixElement{StageID: id, ItemID: "30012", Times: 1000 + i, Quantity: i, StartTime: 1556676000000})
	}
	e.data.catalog = service.NewSEOCatalog(
		[]*model.Item{{ArkItemID: "30012", Type: "MATERIAL", Name: i18n("固源岩", "Orirock Cube", "", ""), Existence: existence("CN")}},
		stages,
		[]*model.Zone{{ArkZoneID: "main_1", ZoneID: 1, Category: "MAINLINE", Name: i18n("z", "z", "z", "z")}},
	)
	e.data.matrices = map[string]*service.SEOMatrix{"CN": service.NewSEOMatrix("CN", rows, lastModified)}
	r := e.get(t, "/en/result/cn/item/30012")
	if n := strings.Count(r.body, `<tr><th scope="row">`); n != itemRowsShown {
		t.Errorf("%d rows, want %d", n, itemRowsShown)
	}
	assertOnce(t, r.body, "by percentage, highest first: 50 of 120 stages")
}

func TestRegister(t *testing.T) {
	shellServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(loadShell(t).Raw())
	}))
	defer shellServer.Close()

	for _, tc := range []struct {
		name     string
		enabled  bool
		shellURL string
		ttl      time.Duration
		status   int
	}{
		// off, the stats paths go back to the frontend: the ingress may still route them here
		{name: "disabled", enabled: false, shellURL: shellServer.URL, ttl: time.Minute, status: 302},
		{name: "invalid shell URL", enabled: true, shellURL: "frontend-rollout/", ttl: time.Minute, status: 302},
		// enabled, it serves the app until the catalog is loaded
		{name: "enabled", enabled: true, shellURL: shellServer.URL, ttl: time.Minute, status: 200},
		// a TTL of 0 used to panic in the goroutine fetching the shell
		{name: "zero TTL", enabled: true, shellURL: shellServer.URL, ttl: 0, status: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conf := &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{
				SEOPagesEnabled:      tc.enabled,
				SEOShellURL:          tc.shellURL,
				SEOShellTTL:          tc.ttl,
				SEOShellFetchTimeout: time.Second,
				SEOSiteURL:           site,
				SEOPageMaxAge:        5 * time.Minute,
			}}
			lc := fxtest.NewLifecycle(t)
			data := service.NewSEOData(lc, &appconfig.Config{}, nil, nil, nil)
			app := fiber.New()
			Register(lc, app, conf, data)
			lc.RequireStart()
			defer lc.RequireStop()
			resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/zh/result/cn/item?a=1", nil), -1)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Errorf("status %d, want %d", resp.StatusCode, tc.status)
			}
			if tc.status == 302 && resp.Header.Get("Location") != "/ZH/result/cn/item?a=1" {
				t.Errorf("Location %q", resp.Header.Get("Location"))
			}
		})
	}
}
