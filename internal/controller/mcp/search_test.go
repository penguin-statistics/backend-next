package mcp

import (
	"slices"
	"sync"
	"testing"

	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/service"
)

func TestSearchKeys(t *testing.T) {
	for _, tc := range []struct{ in, id, name, code string }{
		{"Orirock Cube", "orirock cube", "orirockcube", "orirockcube"},
		{"  ORIROCK-cube ", "orirock-cube", "orirockcube", "orirock-cube"},
		// full-width letters and spaces, as CJK input methods type them
		{"ｏｒｉｒｏｃｋ　ｃｕｂｅ", "orirock cube", "orirockcube", "orirockcube"},
		{"１－７", "1-7", "17", "1-7"},
		// other dashes
		{"1–7", "1-7", "17", "1-7"},
		{"1−7", "1-7", "17", "1-7"},
		// the dashes of codes are kept: 1-12 and 11-2 are two stages
		{"1 - 12", "1 - 12", "112", "1-12"},
		{"Main_01-07", "main_01-07", "main0107", "main01-07"},
		{"覆潮之下・复刻", "覆潮之下・复刻", "覆潮之下复刻", "覆潮之下复刻"},
		{"黑暗时代·上", "黑暗时代·上", "黑暗时代上", "黑暗时代上"},
		// half-width katakana
		{"ｼｮｷｭｳ", "ショキュウ", "ショキュウ", "ショキュウ"},
		{"・ - _", "・ - _", "", "-"},
	} {
		if got := fold(tc.in); got != tc.id {
			t.Errorf("fold(%q) = %q, want %q", tc.in, got, tc.id)
		}
		if got := nameKey(tc.in); got != tc.name {
			t.Errorf("nameKey(%q) = %q, want %q", tc.in, got, tc.name)
		}
		if got := codeKey(tc.in); got != tc.code {
			t.Errorf("codeKey(%q) = %q, want %q", tc.in, got, tc.code)
		}
	}
	if _, err := newQuery("・ - _"); err == nil {
		t.Error("a query of separators only")
	}
}

func TestSearchIndex(t *testing.T) {
	ix := newSearchIndex(testCatalog())
	// the items the frontend lists only
	ids := make([]string, 0, len(ix.items))
	for _, k := range ix.items {
		ids = append(ids, k.item.ArkID)
	}
	if got, want := ids, []string{"30014", "30013", "30012", "30011", "3114", "3113"}; !slices.Equal(got, want) {
		t.Errorf("items %q, want %q", got, want)
	}
	// each key once, readings without their backticks
	var keys []string
	for _, k := range ix.items[2].aliases {
		keys = append(keys, k.by+":"+k.key)
	}
	want := []string{"alias:固源岩", "alias:土块", "alias:石头", "alias:初級源岩", "alias:しょきゅうげんがん", "alias:ショキュウゲンガン",
		"pinyin:guyuanyan", "pinyin:tukuai", "pinyin:shitou", "romaji:shokyuugengan", "romaji:syokyuugengan"}
	if !slices.Equal(keys, want) {
		t.Errorf("keys of 30012 %q, want %q", keys, want)
	}
	// one key for a code the same in every language, and the zones shared by their stages
	if k := ix.stages[0]; k.stage.ArkID != "main_01-07" || len(k.codes) != 1 || k.zone == nil || k.zone.id != "main_1" {
		t.Errorf("keys of main_01-07 %+v", k)
	}
	main := slices.IndexFunc(ix.stages, func(k stageKeys) bool { return k.stage.ArkID == "main_10-01" })
	tough := slices.IndexFunc(ix.stages, func(k stageKeys) bool { return k.stage.ArkID == "tough_10-01" })
	if main < 0 || tough < 0 || ix.stages[main].zone == ix.stages[tough].zone || len(ix.stages[main].zone.names) != 4 {
		t.Fatalf("zones of 10-2 at %d and %d", main, tough)
	}
	// the items of the types not listed, searched only when no listed item matches
	ids = ids[:0]
	for _, k := range ix.unlisted {
		ids = append(ids, k.item.ArkID)
	}
	if got, want := ids, []string{"4001_1000", "recruit_tag_senior_operator", "4005", "30135"}; !slices.Equal(got, want) {
		t.Errorf("unlisted items %q, want %q", got, want)
	}
}

// The index is made once per catalog, which SEOData replaces rather than change.
func TestSearchIndexOncePerCatalog(t *testing.T) {
	tl := &tools{}
	c := testCatalog()
	ix := tl.searchIndex(c)
	if ix.catalog != c || tl.searchIndex(c) != ix {
		t.Error("made again for the same catalog")
	}
	c2 := testCatalog()
	ix2 := tl.searchIndex(c2)
	if ix2 == ix || ix2.catalog != c2 || tl.searchIndex(c2) != ix2 {
		t.Error("not made again for another catalog")
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if tl.searchIndex(c2).catalog != c2 {
				t.Error("index of another catalog")
			}
		})
	}
	wg.Wait()
}

// A search answers from the catalog at hand: the one of the call, after SEOData replaced it.
func TestSearchFollowsTheCatalog(t *testing.T) {
	data := &fakeData{catalog: testCatalog()}
	_, app := testServer(data)
	if out, _ := searchItems(t, app, `{"query":"Carbon Brick"}`); len(out.Results) != 2 {
		t.Fatalf("results %+v", out.Results)
	}
	data.catalog = service.NewSEOCatalog([]*model.Item{
		{ArkItemID: "3114", Type: "MATERIAL", Name: i18n("碳素组", "Carbon Pack", "上級炭素材", "카본 팩"), Existence: existence(all...)},
	}, []*model.Stage{
		{ArkStageID: "main_01-07", ZoneID: 1, Code: i18n("1-7", "1-7", "1-7", "1-7"), Sanity: null.IntFrom(6), Existence: existence(all...)},
	}, nil)
	if out, _ := searchItems(t, app, `{"query":"Carbon Brick"}`); len(out.Results) != 0 {
		t.Errorf("results %+v", out.Results)
	}
	if out, _ := searchItems(t, app, `{"query":"Carbon Pack"}`); len(out.Results) != 1 {
		t.Errorf("results %+v", out.Results)
	}
}
