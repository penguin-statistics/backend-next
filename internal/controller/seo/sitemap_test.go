package seo

import (
	"encoding/xml"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/service"
)

// lastmods parses a sitemap or sitemap index into its URLs and their lastmod ("" for none), in order
func lastmods(t *testing.T, body string) (locs []string, at map[string]string) {
	t.Helper()
	var doc struct {
		URLs     []xmlSitemapURL `xml:"url"`
		Sitemaps []xmlSitemapURL `xml:"sitemap"`
	}
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("the sitemap does not parse: %v\n%s", err, body)
	}
	at = map[string]string{}
	for _, u := range append(doc.URLs, doc.Sitemaps...) {
		if _, ok := at[u.Loc]; ok {
			t.Errorf("%s listed twice", u.Loc)
		}
		locs = append(locs, u.Loc)
		at[u.Loc] = u.LastMod
	}
	return locs, at
}

// warmAll gives every server a matrix: JP and KR without rows
func warmAll(e *testEnv) {
	for _, s := range []string{"JP", "KR"} {
		e.data.matrices[s] = service.NewSEOMatrix(s, nil, lastModified)
	}
}

func (e *testEnv) getWith(t testing.TB, path string, header map[string]string) response {
	t.Helper()
	req := httptest.NewRequest(fiber.MethodGet, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := e.app.Test(req, -1)
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

func TestSitemapIndex(t *testing.T) {
	e := newTestEnv(t)
	r := e.get(t, "/sitemap.xml")
	if r.status != 200 {
		t.Fatalf("status %d", r.status)
	}
	if got := r.header.Get("Content-Type"); got != "application/xml; charset=utf-8" {
		t.Errorf("Content-Type %q", got)
	}
	golden(t, "testdata/sitemap_index.golden.xml", r.body)

	locs, at := lastmods(t, r.body)
	if len(locs) != 16 {
		t.Errorf("%d sitemaps, want 16", len(locs))
	}
	for _, lang := range langs {
		for _, server := range []string{"cn", "us", "jp", "kr"} {
			loc := site + "/sitemap-" + lang + "-" + server + ".xml"
			lm, ok := at[loc]
			if !ok {
				t.Errorf("no %s", loc)
				continue
			}
			// the matrices of JP and KR are not calculated: their sitemaps are 503s until then
			if want := map[bool]string{true: "2026-10-21T10:30:00Z", false: ""}[server == "cn" || server == "us"]; lm != want {
				t.Errorf("%s: lastmod %q, want %q", loc, lm, want)
			}
		}
	}
	// incomplete: cached for a minute, never answered with 304
	if r.header.Get("Cache-Control") != "public, max-age=60" || r.header.Get("Last-Modified") != "" {
		t.Errorf("index while JP and KR are cold: Cache-Control %q, Last-Modified %q", r.header.Get("Cache-Control"), r.header.Get("Last-Modified"))
	}

	warmAll(e)
	r = e.get(t, "/sitemap.xml")
	for k, want := range map[string]string{
		"Content-Type":  "application/xml; charset=utf-8",
		"Cache-Control": "public, max-age=60, s-maxage=300",
		"Last-Modified": "Wed, 21 Oct 2026 10:30:00 GMT",
		"X-Robots-Tag":  "noindex",
		// the headers the pages drop
		"Cross-Origin-Resource-Policy": "",
		"X-Frame-Options":              "",
		"Access-Control-Allow-Origin":  "",
		"Vary":                         "",
	} {
		if got := r.header.Get(k); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
	}
	if _, at := lastmods(t, r.body); at[site+"/sitemap-ko-kr.xml"] != "2026-10-21T10:30:00Z" {
		t.Errorf("ko-kr lastmod %q", at[site+"/sitemap-ko-kr.xml"])
	}
}

func TestSitemap(t *testing.T) {
	e := newTestEnv(t)
	r := e.get(t, "/sitemap-en-cn.xml")
	if r.status != 200 {
		t.Fatalf("status %d", r.status)
	}
	for k, want := range map[string]string{
		"Content-Type":  "application/xml; charset=utf-8",
		"Cache-Control": "public, max-age=60, s-maxage=300",
		"Last-Modified": "Wed, 21 Oct 2026 10:30:00 GMT",
		// a file for crawlers, not a page to find in search results
		"X-Robots-Tag":                 "noindex",
		"Cross-Origin-Embedder-Policy": "",
		"Access-Control-Allow-Origin":  "",
		"Vary":                         "",
	} {
		if got := r.header.Get(k); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
	}
	golden(t, "testdata/sitemap_en_cn.golden.xml", r.body)
	if !strings.HasPrefix(r.body, `<?xml version="1.0" encoding="UTF-8"?>`+"\n"+`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`) {
		t.Errorf("not a urlset: %.100s", r.body)
	}
	for _, absent := range []string{"changefreq", "priority", "xhtml:link"} {
		if strings.Contains(r.body, absent) {
			t.Errorf("%s in the sitemap", absent)
		}
	}

	locs, at := lastmods(t, r.body)
	// the time of the matrix, as the pages give it, closed events (evil, act1_01 and act1_02) too
	want := map[string]string{
		"/en/result/cn/item":                     "2026-10-21T10:30:00Z",
		"/en/result/cn/stage":                    "2026-10-21T10:30:00Z",
		"/en/result/cn/item/30011":               "2026-10-21T10:30:00Z",
		"/en/result/cn/item/30012":               "2026-10-21T10:30:00Z",
		"/en/result/cn/stage/main_1/main_01-07":  "2026-10-21T10:30:00Z",
		"/en/result/cn/item/evil":                "2026-10-21T10:30:00Z",
		"/en/result/cn/stage/act1_zone1/act1_01": "2026-10-21T10:30:00Z",
		"/en/result/cn/stage/act1_zone1/act1_02": "2026-10-21T10:30:00Z",
	}
	if len(locs) != len(want) {
		t.Errorf("%d URLs, want %d: %v", len(locs), len(want), locs)
	}
	for path, lm := range want {
		if got, ok := at[site+path]; !ok || got != lm {
			t.Errorf("%s: lastmod %q (listed: %v), want %q", path, got, ok, lm)
		}
	}
	// not found (30013 is CN only and never dropped on US, tag_1 is no item the frontend lists), or noindex (quiet
	// never dropped, the recruitment statistics)
	for _, absent := range []string{"/item/30013", "/item/tag_1", "/item/quiet", "/stage/recruit/recruit"} {
		if strings.Contains(r.body, absent+"<") {
			t.Errorf("%s in the sitemap", absent)
		}
	}

	r = e.get(t, "/sitemap-ja-us.xml")
	if locs, _ := lastmods(t, r.body); strings.Join(locs, " ") != site+"/ja/result/us/item "+site+"/ja/result/us/stage "+
		site+"/ja/result/us/item/30012 "+site+"/ja/result/us/stage/main_1/main_01-07" {
		t.Errorf("ja-us: %v", locs)
	}

	// HEAD, for crawlers checking the sitemap
	r = e.do(t, fiber.MethodHead, "/sitemap-en-cn.xml")
	if r.status != 200 || r.body != "" || r.header.Get("Content-Type") != "application/xml; charset=utf-8" {
		t.Errorf("HEAD: %d %q", r.status, r.header.Get("Content-Type"))
	}
}

// sitemapURLs fetches the index and every sitemap it lists, and returns the URLs they list
func sitemapURLs(t *testing.T, e *testEnv) map[string]bool {
	t.Helper()
	urls := map[string]bool{}
	sitemaps, _ := lastmods(t, e.get(t, "/sitemap.xml").body)
	for _, sm := range sitemaps {
		r := e.get(t, strings.TrimPrefix(sm, site))
		if r.status != 200 {
			t.Errorf("%s: status %d", sm, r.status)
			continue
		}
		locs, _ := lastmods(t, r.body)
		for _, loc := range locs {
			urls[loc] = true
		}
	}
	return urls
}

// assertSitemapsListExactlyTheIndexablePages checks the sitemaps against the pages: every URL listed is served as an
// indexable 200 with itself as canonical (never a 404, a redirect or a noindex page, which search engines report as
// errors of the sitemap), and every page of the catalog served so is listed.
func assertSitemapsListExactlyTheIndexablePages(t *testing.T, e *testEnv) {
	t.Helper()
	urls := sitemapURLs(t, e)
	if len(urls) == 0 {
		t.Fatal("no URLs")
	}
	for u := range urls {
		r := e.get(t, strings.TrimPrefix(u, site))
		if r.status != 200 || r.header.Get("X-Robots-Tag") != "" {
			t.Errorf("%s: status %d, X-Robots-Tag %q", u, r.status, r.header.Get("X-Robots-Tag"))
			continue
		}
		assertOnce(t, r.body, `<meta name="robots" content="index,follow">`)
		if m := canonicalRe.FindAllStringSubmatch(r.body, -1); len(m) != 1 || html.UnescapeString(m[0][1]) != u {
			t.Errorf("%s: canonical %v", u, m)
		}
	}

	// the other way round: the pages of every item and stage of the catalog, in every language and server
	paths := []string{"/item", "/stage"}
	for _, item := range e.data.catalog.ItemList {
		paths = append(paths, "/item/"+url.PathEscape(item.ArkID))
	}
	for _, stage := range e.data.catalog.StageList {
		zone := "none"
		if stage.Zone != nil {
			zone = stage.Zone.ArkID
		}
		paths = append(paths, "/stage/"+url.PathEscape(zone)+"/"+url.PathEscape(stage.ArkID))
	}
	indexable := map[string]bool{}
	for _, lang := range langs {
		for _, server := range []string{"cn", "us", "jp", "kr"} {
			for _, p := range paths {
				r := e.get(t, "/"+lang+"/result/"+server+p)
				if r.status != 200 || !strings.Contains(r.body, `<meta name="robots" content="index,follow">`) {
					continue
				}
				m := canonicalRe.FindStringSubmatch(r.body)
				if m == nil {
					t.Errorf("%s: indexable without canonical", p)
					continue
				}
				// clients remove the dot segments of a URL before requesting it (RFC 3986 5.2.4): the page of
				// item "." is never requested as such
				if u := html.UnescapeString(m[1]); !strings.Contains(u+"/", "/./") && !strings.Contains(u+"/", "/../") {
					indexable[u] = true
				}
			}
		}
	}
	for u := range indexable {
		if !urls[u] {
			t.Errorf("%s is an indexable page, not listed", u)
		}
	}
	if len(indexable) != len(urls) {
		t.Errorf("%d indexable pages, %d URLs listed", len(indexable), len(urls))
	}
}

func TestSitemapsListExactlyTheIndexablePages(t *testing.T) {
	e := newTestEnv(t)
	warmAll(e)
	assertSitemapsListExactlyTheIndexablePages(t, e)

	// IDs that must be escaped in the path and in XML, or that do not route as themselves, and a stage in no zone
	odd := []string{"a&b", "a/b", "é <x>", "", ".", ".."}
	var items []*model.Item
	var rows []*modelv2.OneDropMatrixElement
	for i, id := range odd {
		items = append(items, &model.Item{ArkItemID: id, Type: "MATERIAL", SortID: i, Name: i18n(id, id, id, id), Existence: existence("CN")})
		rows = append(rows, &modelv2.OneDropMatrixElement{StageID: "main_01-07", ItemID: id, Times: 10, Quantity: 1, StartTime: 1556676000000})
	}
	rows = append(rows, &modelv2.OneDropMatrixElement{StageID: "orphan", ItemID: "a&b", Times: 10, Quantity: 1, StartTime: 1556676000000})
	e.data.catalog = service.NewSEOCatalog(items,
		[]*model.Stage{
			{ArkStageID: "main_01-07", StageID: 1, ZoneID: 1, Code: i18n("1-7", "1-7", "1-7", "1-7"), Existence: existence(all...)},
			{ArkStageID: "orphan", StageID: 2, ZoneID: 99, Code: i18n("X", "X", "X", "X"), Existence: existence(all...)},
		},
		[]*model.Zone{{ArkZoneID: "main_1", ZoneID: 1, Category: "MAINLINE", Name: i18n("z", "z", "z", "z"), Existence: existence(all...)}},
	)
	e.data.matrices["CN"] = service.NewSEOMatrix("CN", rows, lastModified)
	assertSitemapsListExactlyTheIndexablePages(t, e)

	locs, _ := lastmods(t, e.get(t, "/sitemap-en-cn.xml").body)
	if strings.Join(locs, " ") != site+"/en/result/cn/item "+site+"/en/result/cn/stage "+site+"/en/result/cn/item/a&b "+
		site+"/en/result/cn/item/a%2Fb "+site+"/en/result/cn/item/%C3%A9%20%3Cx%3E "+site+"/en/result/cn/stage/main_1/main_01-07" {
		t.Errorf("en-cn: %v", locs)
	}
	if body := e.get(t, "/sitemap-en-cn.xml").body; !strings.Contains(body, "/item/a&amp;b</loc>") {
		t.Error("the URLs are not escaped in XML")
	}
}

func TestSitemapLastmod(t *testing.T) {
	e := newTestEnv(t)
	warmAll(e)
	// every page has the time of the matrix, as its Last-Modified, dateModified and "updated" date have
	_, at := lastmods(t, e.get(t, "/sitemap-zh-cn.xml").body)
	for loc, got := range at {
		if got != "2026-10-21T10:30:00Z" {
			t.Errorf("%s: lastmod %q", loc, got)
		}
	}
	page := e.get(t, "/zh/result/cn/item/evil")
	if page.header.Get("Last-Modified") != "Wed, 21 Oct 2026 10:30:00 GMT" || !strings.Contains(page.body, `"dateModified":"2026-10-21T10:30:00Z"`) {
		t.Errorf("the page of evil: Last-Modified %q", page.header.Get("Last-Modified"))
	}

	// a catalog that changed after the matrix changed the sitemaps, not the lastmod of the pages
	changed := lastModified.Add(10 * time.Minute)
	catalog := testCatalog()
	catalog.ChangedAt = changed
	e.data.catalog = catalog
	r := e.get(t, "/sitemap-zh-cn.xml")
	if r.header.Get("Last-Modified") != changed.Format(http.TimeFormat) || strings.Contains(r.body, "2026-10-21T10:40:00Z") {
		t.Errorf("catalog changed after the matrix: Last-Modified %q", r.header.Get("Last-Modified"))
	}
	r = e.get(t, "/sitemap.xml")
	if _, at := lastmods(t, r.body); at[site+"/sitemap-zh-cn.xml"] != "2026-10-21T10:40:00Z" || r.header.Get("Last-Modified") != changed.Format(http.TimeFormat) {
		t.Errorf("index after the catalog changed: lastmod %q, Last-Modified %q", at[site+"/sitemap-zh-cn.xml"], r.header.Get("Last-Modified"))
	}
	// one that changed before it
	before := newTestEnv(t)
	warmAll(before)
	before.data.catalog.ChangedAt = lastModified.Add(-time.Hour)
	if r := before.get(t, "/sitemap-zh-cn.xml"); r.header.Get("Last-Modified") != "Wed, 21 Oct 2026 10:30:00 GMT" {
		t.Errorf("catalog changed before the matrix: Last-Modified %q", r.header.Get("Last-Modified"))
	}

	// without a last modified time of the matrix, no lastmod is known
	e.data.matrices["CN"] = service.NewSEOMatrix("CN", []*modelv2.OneDropMatrixElement{
		{StageID: "act1_01", ItemID: "evil", Times: 10, Quantity: 1, StartTime: 1688025600000, EndTime: null.IntFrom(1689105600000)},
	}, time.Time{})
	r = e.get(t, "/sitemap-zh-cn.xml")
	if strings.Contains(r.body, "<lastmod>") || r.header.Get("Last-Modified") != "" || r.header.Get("Cache-Control") != "public, max-age=60" {
		t.Errorf("lastmod without the matrix's time: Last-Modified %q, Cache-Control %q\n%s", r.header.Get("Last-Modified"), r.header.Get("Cache-Control"), r.body)
	}
}

func TestSitemapNotModified(t *testing.T) {
	e := newTestEnv(t)
	warmAll(e)
	for _, path := range []string{"/sitemap.xml", "/sitemap-en-cn.xml"} {
		r := e.get(t, path)
		lm := r.header.Get("Last-Modified")
		if lm == "" {
			t.Fatalf("%s: no Last-Modified", path)
		}
		r = e.getWith(t, path, map[string]string{"If-Modified-Since": lm})
		if r.status != 304 || r.body != "" || r.header.Get("Last-Modified") != lm || r.header.Get("Cache-Control") != "public, max-age=60, s-maxage=300" {
			t.Errorf("%s: status %d, %d bytes, Last-Modified %q, Cache-Control %q", path, r.status, len(r.body), r.header.Get("Last-Modified"), r.header.Get("Cache-Control"))
		}
		older := lastModified.Add(-time.Second).Format(http.TimeFormat)
		if r := e.getWith(t, path, map[string]string{"If-Modified-Since": older}); r.status != 200 || r.body == "" {
			t.Errorf("%s modified since %s: status %d", path, older, r.status)
		}
	}
	// a new catalog, as when a stage is added: the sitemap lists it, so it was modified
	catalog := testCatalog()
	catalog.ChangedAt = lastModified.Add(time.Minute)
	e.data.catalog = catalog
	if r := e.getWith(t, "/sitemap-en-cn.xml", map[string]string{"If-Modified-Since": "Wed, 21 Oct 2026 10:30:00 GMT"}); r.status != 200 {
		t.Errorf("new catalog: status %d", r.status)
	}
	// a new matrix
	e.data.matrices["CN"] = service.NewSEOMatrix("CN", nil, lastModified.Add(30*time.Minute))
	if r := e.getWith(t, "/sitemap-en-cn.xml", map[string]string{"If-Modified-Since": "Wed, 21 Oct 2026 10:31:00 GMT"}); r.status != 200 {
		t.Errorf("new matrix: status %d", r.status)
	}
	// an index listing cold sitemaps has no Last-Modified to compare with
	e.data.matrices["KR"] = nil
	if r := e.getWith(t, "/sitemap.xml", map[string]string{"If-Modified-Since": "Wed, 21 Oct 2026 12:00:00 GMT"}); r.status != 200 {
		t.Errorf("index with KR cold: status %d", r.status)
	}
}

func TestSitemapNotModifiedAfterAChangeInTheSameSecond(t *testing.T) {
	e := newTestEnv(t)
	warmAll(e)
	// the matrix of server published anew at t, with the same rows
	matrixAt := func(server string, t time.Time) *service.SEOMatrix {
		m := *testMatrices()[server]
		m.LastModified = t
		return &m
	}
	e.data.matrices["CN"] = matrixAt("CN", lastModified.Add(100*time.Millisecond))
	since := func(lm string) map[string]string { return map[string]string{"If-Modified-Since": lm} }
	first := e.get(t, "/sitemap-en-cn.xml")
	if lm := first.header.Get("Last-Modified"); lm != "Wed, 21 Oct 2026 10:30:00 GMT" {
		t.Fatalf("Last-Modified %q", lm)
	}
	if r := e.get(t, "/sitemap-en-cn.xml"); r.header.Get("Last-Modified") != first.header.Get("Last-Modified") {
		t.Errorf("unchanged: Last-Modified %q", r.header.Get("Last-Modified"))
	}

	// 1-7 moved to another zone later in the same second: modified since the first sitemap and index
	catalog := testCatalog()
	catalog.Stages["main_01-07"].Zone = catalog.Zones["act1_zone1"]
	catalog.ChangedAt = lastModified.Add(900 * time.Millisecond)
	e.data.catalog = catalog
	r := e.getWith(t, "/sitemap-en-cn.xml", since(first.header.Get("Last-Modified")))
	if r.status != 200 || r.body == first.body || r.header.Get("Last-Modified") != "Wed, 21 Oct 2026 10:30:01 GMT" {
		t.Errorf("catalog changed in the same second: status %d, Last-Modified %q", r.status, r.header.Get("Last-Modified"))
	}
	// a sitemap built for the first time since then, which a crawler has from another backend
	if r := e.getWith(t, "/sitemap-zh-cn.xml", since(first.header.Get("Last-Modified"))); r.status != 200 || r.header.Get("Last-Modified") != "Wed, 21 Oct 2026 10:30:01 GMT" {
		t.Errorf("another sitemap after the catalog changed in the same second: status %d, Last-Modified %q", r.status, r.header.Get("Last-Modified"))
	}
	index := e.getWith(t, "/sitemap.xml", since(first.header.Get("Last-Modified")))
	if index.status != 200 || index.header.Get("Last-Modified") != "Wed, 21 Oct 2026 10:30:01 GMT" {
		t.Errorf("index after the catalog changed in the same second: status %d, Last-Modified %q", index.status, index.header.Get("Last-Modified"))
	}

	// the matrix published in that second too, after the catalog
	e.data.matrices["CN"] = matrixAt("CN", lastModified.Add(950*time.Millisecond))
	if r := e.getWith(t, "/sitemap-en-cn.xml", since("Wed, 21 Oct 2026 10:30:01 GMT")); r.status != 200 || r.header.Get("Last-Modified") != "Wed, 21 Oct 2026 10:30:02 GMT" {
		t.Errorf("matrix published in the same second: status %d, Last-Modified %q", r.status, r.header.Get("Last-Modified"))
	}
	index = e.get(t, "/sitemap.xml")

	// the matrix of another server, with a time older than the latest sitemap: the index lists its new lastmod
	e.data.matrices["US"] = matrixAt("US", lastModified.Add(500*time.Millisecond))
	if r := e.getWith(t, "/sitemap.xml", since(index.header.Get("Last-Modified"))); r.status != 200 || r.header.Get("Last-Modified") != "Wed, 21 Oct 2026 10:30:03 GMT" {
		t.Errorf("index after an older matrix of another server: status %d, Last-Modified %q", r.status, r.header.Get("Last-Modified"))
	}
	if r := e.getWith(t, "/sitemap.xml", since("Wed, 21 Oct 2026 10:30:03 GMT")); r.status != 304 {
		t.Errorf("unchanged index: status %d", r.status)
	}

	// after a start, a matrix seen first without a time (read between its time and its rows at its first
	// publication), then with one in the second of the index: the index lists its lastmod from then on
	start := newTestEnv(t)
	warmAll(start)
	start.data.matrices["JP"] = service.NewSEOMatrix("JP", nil, time.Time{})
	index = start.get(t, "/sitemap.xml")
	if lm := index.header.Get("Last-Modified"); lm != "Wed, 21 Oct 2026 10:30:00 GMT" {
		t.Fatalf("index with a matrix without a time: Last-Modified %q", lm)
	}
	start.data.matrices["JP"] = service.NewSEOMatrix("JP", nil, lastModified.Add(300*time.Millisecond))
	if r := start.getWith(t, "/sitemap.xml", since(index.header.Get("Last-Modified"))); r.status != 200 || r.header.Get("Last-Modified") != "Wed, 21 Oct 2026 10:30:01 GMT" {
		t.Errorf("index after the matrix got a time: status %d, Last-Modified %q", r.status, r.header.Get("Last-Modified"))
	}
}

// a request still holding a replaced catalog, answered after a request for the current one built its sitemap but
// before it kept it: the current sitemap is modified since the one sent from the replaced catalog
func TestSitemapModifiedSinceAReplacedCatalog(t *testing.T) {
	e := newTestEnv(t)
	h := NewHandler(site+"/", 5*time.Minute, e.shells, e.data)
	e.app = fiber.New(fiber.Config{Immutable: true})
	h.Mount(e.app)
	replaced := testCatalog()
	replaced.ChangedAt = lastModified.Add(time.Minute)
	current := testCatalog()
	current.ChangedAt = lastModified.Add(2 * time.Minute)
	current.Stages["main_01-07"].Zone = current.Zones["act1_zone1"]
	e.data.catalog = current
	k := sitemapKey{lang: "en", server: "CN"}
	matrix := e.data.matrices["CN"]

	// the request for the current catalog builds its sitemap, which is given its time
	built := h.sitemap(k, current, matrix)
	h.sitemapLastModified(k.server, current, matrix)
	// the request holding the replaced catalog then builds, keeps and sends its own
	e.app.Get("/replaced", func(c *fiber.Ctx) error {
		h.sendSitemap(c, kindSitemap, h.sitemap(k, replaced, matrix).xml, h.sitemapLastModified(k.server, replaced, matrix))
		return nil
	})
	old := e.get(t, "/replaced")
	lm := old.header.Get("Last-Modified")
	if lm == "" {
		t.Fatal("the sitemap of the replaced catalog has no Last-Modified")
	}
	// and the request for the current catalog keeps its sitemap last
	h.sitemaps.Store(k, built)

	r := e.getWith(t, "/sitemap-en-cn.xml", map[string]string{"If-Modified-Since": lm})
	if r.status != 200 || r.body == old.body {
		t.Fatalf("current sitemap since the one of the replaced catalog (%s): status %d, Last-Modified %q", lm, r.status, r.header.Get("Last-Modified"))
	}
	if r := e.getWith(t, "/sitemap-en-cn.xml", map[string]string{"If-Modified-Since": r.header.Get("Last-Modified")}); r.status != 304 {
		t.Errorf("unchanged since: status %d", r.status)
	}
}

func TestSitemapLastModifiedNotLaterThanDate(t *testing.T) {
	e := newTestEnv(t)
	warmAll(e)
	// a matrix whose changes were recorded in the next seconds
	e.data.matrices["CN"] = service.NewSEOMatrix("CN", nil, time.Now().Add(time.Minute))
	for _, path := range []string{"/sitemap-en-cn.xml", "/sitemap.xml"} {
		r := e.get(t, path)
		if r.status != 200 || r.header.Get("Last-Modified") != "" || r.header.Get("Cache-Control") != "public, max-age=60, s-maxage=300" {
			t.Errorf("%s: status %d, Last-Modified %q, Cache-Control %q", path, r.status, r.header.Get("Last-Modified"), r.header.Get("Cache-Control"))
		}
	}
}

func TestSitemapsCold(t *testing.T) {
	e := newTestEnv(t)
	e.data.matrices = map[string]*service.SEOMatrix{}
	assertCold := func(path string) {
		t.Helper()
		r := e.get(t, path)
		if r.status != 503 || r.header.Get("Retry-After") != "120" || r.header.Get("Cache-Control") != "no-store" ||
			r.header.Get("Last-Modified") != "" || strings.Contains(r.body, "<urlset") {
			t.Errorf("%s: status %d, Retry-After %q, Cache-Control %q", path, r.status, r.header.Get("Retry-After"), r.header.Get("Cache-Control"))
		}
	}
	assertCold("/sitemap-en-cn.xml")
	// the index lists the sitemaps all the same, without lastmod
	r := e.get(t, "/sitemap.xml")
	if locs, at := lastmods(t, r.body); r.status != 200 || len(locs) != 16 || at[site+"/sitemap-en-cn.xml"] != "" {
		t.Errorf("index while cold: status %d, %d sitemaps, %v", r.status, len(locs), at)
	}

	e.data.matrices = testMatrices()
	e.data.catalog = nil
	assertCold("/sitemap-en-cn.xml")
	assertCold("/sitemap.xml")
}

func TestSitemapNotFound(t *testing.T) {
	e := newTestEnv(t)
	for _, path := range []string{"/sitemap-en-CN.xml", "/SITEMAP-en-cn.xml", "/sitemap-xx-cn.xml", "/sitemap-en-xx.xml",
		"/sitemap-en.xml", "/sitemap-en-cn.xml.gz", "/sitemap-en-cn", "/sitemap-", "/sitemap-en-cn.xml/x", "/sitemap-en-cn.xml/",
		"/SITEMAP.XML", "/Sitemap.xml", "/sitemap.xml/"} {
		r := e.get(t, path)
		if r.status != 404 || r.header.Get("Content-Type") != "text/plain; charset=utf-8" || r.header.Get("Cache-Control") != "public, max-age=60" {
			t.Errorf("%s: status %d, Content-Type %q, Cache-Control %q", path, r.status, r.header.Get("Content-Type"), r.header.Get("Cache-Control"))
		}
	}
}

func TestSitemapsDisabled(t *testing.T) {
	app := fiber.New()
	MountDisabled(app)
	for _, path := range []string{"/sitemap.xml", "/sitemap-en-cn.xml"} {
		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, path, nil), -1)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 || resp.Header.Get("Content-Type") != "text/plain; charset=utf-8" || resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: status %d, Content-Type %q, Cache-Control %q", path, resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Cache-Control"))
		}
	}
}
