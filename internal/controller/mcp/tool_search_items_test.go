package mcp

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/service"
)

// searchItems calls search_items with args, and returns its result, which must not be an error.
func searchItems(t *testing.T, app *fiber.App, args string) (itemSearch, string) {
	t.Helper()
	res := callTool(t, app, "search_items", args)
	if res.IsError {
		t.Fatalf("%s: error: %s", args, res.text(t))
	}
	var out itemSearch
	if err := json.Unmarshal(res.StructuredContent, &out); err != nil {
		t.Fatal(err)
	}
	return out, res.text(t)
}

// The collisions of the names and aliases of the catalog of 2026-10-22.
func TestSearchItems(t *testing.T) {
	_, app := testServer(&fakeData{catalog: testCatalog()})
	for _, tc := range []struct {
		query string
		// the results: ID, matchedBy, matchType and matchedText
		want      []string
		ambiguous bool
	}{
		// the exact name before the name it begins
		{"固源岩", []string{"30012 name exact 固源岩", "30013 name prefix 固源岩组"}, false},
		// an alias of the four tiers: each of them, lowest rarity first
		{"石头", []string{"30011 alias exact 石头", "30012 alias exact 石头", "30013 alias exact 石头", "30014 alias exact 石头"}, true},
		{"土块", []string{"30011 alias exact 土块", "30012 alias exact 土块", "30013 alias exact 土块", "30014 alias exact 土块"}, true},
		// readings without their backticks, spaces or not
		{"guyuanyan", []string{"30012 pinyin exact guyuanyan", "30013 pinyin prefix guyuanyanzu"}, false},
		{"gu yuan yan", []string{"30012 pinyin exact guyuanyan", "30013 pinyin prefix guyuanyanzu"}, false},
		{"shokyuu", []string{"30012 romaji prefix shokyuugengan"}, false},
		// readings match whole or by prefix only: yuan is in guyuanyan and tichunyuanyan too
		{"yuan", []string{"30011 pinyin prefix yuanyan"}, false},
		{"zu", nil, false},
		// the names in every language, whatever the case, spaces, dashes or width
		{"Orirock Cube", []string{"30012 name exact Orirock Cube"}, false},
		{"orirock-cube", []string{"30012 name exact Orirock Cube"}, false},
		{"ＯＲＩＲＯＣＫ　ＣＵＢＥ", []string{"30012 name exact Orirock Cube"}, false},
		{"初級源岩", []string{"30012 name exact 初級源岩"}, false},
		{"원암 큐브", []string{"30012 name exact 원암 큐브", "30013 name prefix 원암 큐브 번들"}, false},
		{"orirock", []string{"30011 name exact Orirock", "30012 name prefix Orirock Cube", "30013 name prefix Orirock Cluster",
			"30014 name prefix Orirock Concentration"}, false},
		{"源岩", []string{"30011 name exact 源岩", "30012 name substring 固源岩", "30013 name substring 固源岩组",
			"30014 name substring 提纯源岩"}, false},
		{"しょきゅう", []string{"30012 alias prefix しょきゅうげんがん"}, false},
		{"30012", []string{"30012 id exact 30012"}, false},
		// the en names of 3113 and 3114 are the same in the data
		{"Carbon Brick", []string{"3113 name exact Carbon Brick", "3114 name exact Carbon Brick"}, true},
		// the items of the types not listed, when no listed item matches
		{"钱", []string{"4001_1000 alias exact 钱"}, false},
		{"LMD1000", []string{"4001_1000 name exact LMD1000"}, false},
		{"4001_1000", []string{"4001_1000 id exact 4001_1000"}, false},
		{"D32 Steel", []string{"30135 name exact D32 Steel"}, false},
		{"D32钢", []string{"30135 name exact D32钢"}, false},
		{"Orundum", nil, false},
	} {
		out, _ := searchItems(t, app, `{"query":`+strconv.Quote(tc.query)+`}`)
		got := make([]string, 0, len(out.Results))
		for _, r := range out.Results {
			got = append(got, strings.Join([]string{r.ItemID, r.MatchedBy, r.MatchType, r.MatchedText}, " "))
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: results %q, want %q", tc.query, got, tc.want)
		}
		if out.Ambiguous != tc.ambiguous || out.Truncated || out.Query != tc.query || out.Results == nil {
			t.Errorf("%s: ambiguous %v, truncated %v, query %q, results %v", tc.query, out.Ambiguous, out.Truncated, out.Query, out.Results)
		}
	}
}

func TestSearchItemsResult(t *testing.T) {
	_, app := testServer(&fakeData{catalog: testCatalog()})
	out, text := searchItems(t, app, `{"query":"orirock cube","lang":"ja"}`)
	if len(out.Results) != 1 {
		t.Fatalf("results %+v", out.Results)
	}
	r := out.Results[0]
	if r.ItemID != "30012" || r.Name != "初級源岩" || r.Names != (itemNames{Zh: "固源岩", En: "Orirock Cube", Ja: "初級源岩", Ko: "원암 큐브"}) ||
		r.Type != "MATERIAL" || r.Rarity != 1 || !slices.Equal(r.ExistsOn, all) ||
		r.PageURL == nil || *r.PageURL != "https://penguin-stats.io/ja/result/cn/item/30012" {
		t.Errorf("result %+v", r)
	}
	if out.Source != (&tools{site: site}).source("") {
		t.Errorf("source %+v", out.Source)
	}
	for _, s := range []string{
		`1 item matches "orirock cube".`,
		"| itemId | name | type | rarity (0-4) | matched by |",
		"| 30012 | 初級源岩 | MATERIAL | 1 | name, exact: Orirock Cube |",
		"Data: Penguin Statistics (https://penguin-stats.io), CC BY-NC 4.0 (https://creativecommons.org/licenses/by-nc/4.0/). Source: https://penguin-stats.io",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("text lacks %q:\n%s", s, text)
		}
	}
	if strings.Contains(text, `"itemId"`) {
		t.Errorf("text holds the JSON:\n%s", text)
	}

	// several as good: the text says so
	_, text = searchItems(t, app, `{"query":"石头"}`)
	for _, s := range []string{
		`4 items match "石头". The first 4 match equally well (alias, exact: 石头): ask which one is meant, or pick by context.`,
		"| 30014 | Orirock Concentration | MATERIAL | 3 | alias, exact: 石头 |",
		"| 30011 | Orirock | MATERIAL | 0 | alias, exact: 石头 |",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("text lacks %q:\n%s", s, text)
		}
	}

	// more than the limit
	out, text = searchItems(t, app, `{"query":"orirock","limit":2}`)
	if len(out.Results) != 2 || !out.Truncated || out.Results[0].ItemID != "30011" || out.Results[1].ItemID != "30012" {
		t.Errorf("results %+v, truncated %v", out.Results, out.Truncated)
	}
	if !strings.Contains(text, `4 items match "orirock". Showing the first 2: narrow the query, or raise limit (at most 25).`) {
		t.Errorf("text:\n%s", text)
	}

	// an item of a type not listed: a note says what it is, and it has no page
	out, text = searchItems(t, app, `{"query":"D32 Steel"}`)
	if len(out.Results) != 1 || out.Results[0].Type != "ARKPLANNER" || out.Results[0].PageURL != nil ||
		!strings.Contains(out.Note, "crafted in the workshop") {
		t.Errorf("result %+v", out)
	}
	if !strings.Contains(text, out.Note) || strings.Contains(text, "Try its name") {
		t.Errorf("text:\n%s", text)
	}

	// none: an empty result, not an error
	out, text = searchItems(t, app, `{"query":"Orundum"}`)
	if len(out.Results) != 0 || !strings.HasPrefix(text, `No item matches "Orundum". Try its name in another language`) ||
		!strings.Contains(string(mustMarshal(t, out)), `"results":[]`) {
		t.Errorf("result %+v, text:\n%s", out, text)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The page of an item is on CN, or else on the first server it exists on.
func TestSearchItemsPage(t *testing.T) {
	_, app := testServer(&fakeData{catalog: service.NewSEOCatalog([]*model.Item{
		{ArkItemID: "jp_only", Type: "ACTIVITY_ITEM", Name: i18n("物", "Thing", "物", "물건"), Existence: existence("JP", "KR")},
		{ArkItemID: "gone", Type: "ACTIVITY_ITEM", Name: i18n("物", "Thing", "物", "물건"), Existence: existence()},
	}, nil, nil)})
	out, _ := searchItems(t, app, `{"query":"thing"}`)
	if len(out.Results) != 2 {
		t.Fatalf("results %+v", out.Results)
	}
	if r := out.Results[0]; !slices.Equal(r.ExistsOn, []string{"JP", "KR"}) || r.PageURL == nil || *r.PageURL != "https://penguin-stats.io/en/result/jp/item/jp_only" {
		t.Errorf("result %+v", r)
	}
	if r := out.Results[1]; len(r.ExistsOn) != 0 || r.PageURL != nil {
		t.Errorf("result %+v", r)
	}
	if b := mustMarshal(t, out.Results[1]); !strings.Contains(string(b), `"existsOn":[],`) || !strings.Contains(string(b), `"pageUrl":null`) {
		t.Errorf("result %s", b)
	}
}

func TestSearchItemsBadInput(t *testing.T) {
	_, app := testServer(&fakeData{catalog: testCatalog()})
	for _, args := range []string{
		`{"query":"・ _ -"}`,
		`{"query":""}`,
		`{"query":"` + strings.Repeat("石", queryMaxLength+1) + `"}`,
		`{"query":"orirock","limit":0}`,
		`{"query":"orirock","limit":26}`,
		`{"query":"orirock","lang":"fr"}`,
		`{}`,
	} {
		if res := callTool(t, app, "search_items", args); !res.IsError {
			t.Errorf("%s: not an error: %s", args, res.StructuredContent)
		}
	}
	// no arguments at all: missing properties, as {} (see observe)
	if res := callTool(t, app, "search_items", "null"); !res.IsError {
		t.Errorf("null: not an error: %s", res.StructuredContent)
	}
	res := callTool(t, app, "search_items", `{"query":"・ _ -"}`)
	if text := res.text(t); !strings.Contains(text, `nothing to search for in the query "・ _ -"`) {
		t.Errorf("text %q", text)
	}
	// 64 characters of any kind
	if res := callTool(t, app, "search_items", `{"query":"`+strings.Repeat("石", queryMaxLength)+`"}`); res.IsError {
		t.Errorf("a query of %d characters: %s", queryMaxLength, res.text(t))
	}
}

func TestSearchItemsWhileCold(t *testing.T) {
	_, app := testServer(&fakeData{})
	if res := callTool(t, app, "search_items", `{"query":"orirock"}`); !res.IsError || res.text(t) != errCold.Error() {
		t.Errorf("result %+v", res)
	}
}

// The most results with the longest names stay far below the budget of a result (about 25k tokens).
func TestSearchItemsSize(t *testing.T) {
	long := strings.Repeat("源", queryMaxLength-2)
	items := make([]*model.Item, 40)
	for i := range items {
		n := long + strconv.Itoa(10+i)
		items[i] = &model.Item{ArkItemID: "item_" + strconv.Itoa(i), Type: "MATERIAL", SortID: i, Name: i18n(n, n, n, n), Existence: existence(all...),
			Keywords: keywords([]string{n, n + "a"}, []string{n + "b"}, []string{"yuan`" + n}, nil)}
	}
	_, app := testServer(&fakeData{catalog: service.NewSEOCatalog(items, nil, nil)})
	res := callTool(t, app, "search_items", `{"query":"源","limit":25}`)
	var out itemSearch
	if err := json.Unmarshal(res.StructuredContent, &out); err != nil || len(out.Results) != 25 || !out.Truncated {
		t.Fatalf("result %s", res.StructuredContent)
	}
	if n := len(res.StructuredContent) + len(res.text(t)); n > 60<<10 {
		t.Errorf("result of %d bytes", n)
	}
}
