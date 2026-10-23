package service

import (
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
)

func TestSEOExistenceOpenAt(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	ms := now.UnixMilli()
	e := SEOExistence{}
	set := func(server string, exist bool, open, close null.Int) {
		v := e[server]
		v.Exist, v.OpenTime, v.CloseTime = exist, open, close
		e[server] = v
	}
	none := null.Int{}
	set("permanent", true, none, none)
	set("absent", false, none, none)
	set("absent_open", false, null.IntFrom(ms-1), null.IntFrom(ms+1))
	set("running", true, null.IntFrom(ms-1), null.IntFrom(ms+1))
	set("upcoming", true, null.IntFrom(ms+1), null.IntFrom(ms+2))
	set("closed", true, null.IntFrom(ms-2), null.IntFrom(ms-1))
	// [open, close)
	set("opens_now", true, null.IntFrom(ms), null.IntFrom(ms+1))
	set("closes_now", true, null.IntFrom(ms-1), null.IntFrom(ms))
	// 0 is no time, as to the frontend
	set("zero_times", true, null.IntFrom(0), null.IntFrom(0))
	set("never_closes", true, null.IntFrom(ms-1), null.IntFrom(constant.FakeEndTimeMilli))

	for server, want := range map[string]bool{
		"permanent": true, "absent": false, "absent_open": false, "running": true, "upcoming": false, "closed": false,
		"opens_now": true, "closes_now": false, "zero_times": true, "never_closes": true, "unknown": false,
	} {
		if got := e.OpenAt(server, now); got != want {
			t.Errorf("%s: OpenAt = %v, want %v", server, got, want)
		}
	}
	// the sentinel is no close time even once it passed
	if !e.OpenAt("never_closes", time.UnixMilli(constant.FakeEndTimeMilli+1)) {
		t.Error("closed at the sentinel close time")
	}
	if (SEOExistence(nil)).OpenAt("CN", now) {
		t.Error("nil existence is open")
	}
}

func TestSEOCatalogParsesTheNewFields(t *testing.T) {
	c := NewSEOCatalog(
		[]*model.Item{
			{ArkItemID: "30012", Type: "MATERIAL", Rarity: 1, Name: []byte(`{"zh":"固源岩"}`),
				Keywords: []byte(`{"alias":{"zh":["固源岩","土块","石头"],"ja":["初級源岩"]},"pron":{"zh":["gu` + "`" + `yuan` + "`" + `yan","tu` + "`" + `kuai"]}}`)},
			{ArkItemID: "nokeywords", Type: "MATERIAL", Keywords: []byte(`null`)},
			{ArkItemID: "malformed", Type: "MATERIAL", Name: []byte(`{"en":"Still Named"}`), Keywords: []byte(`{"alias":{"zh":"not a list"}}`)},
		},
		[]*model.Stage{
			{ArkStageID: "main_01-07", StageID: 1, ZoneID: 1, StageType: "MAIN", Sanity: null.IntFrom(6)},
			{ArkStageID: "gacha_1", StageID: 2, ZoneID: 2, StageType: "ACTIVITY", ExtraProcessType: null.StringFrom("GACHABOX")},
		},
		[]*model.Zone{
			{ArkZoneID: "main_1", ZoneID: 1, Category: "MAINLINE"},
			{ArkZoneID: "act52side_zone1", ZoneID: 2, Category: "ACTIVITY",
				Existence: []byte(`{"CN":{"exist":true,"openTime":1791518400000,"closeTime":1792699200000},"JP":{"exist":false}}`)},
		},
	)
	item := c.Items["30012"]
	if item.Rarity != 1 || len(item.Alias["zh"]) != 3 || item.Alias["zh"][1] != "土块" || item.Alias["ja"][0] != "初級源岩" ||
		item.Pron["zh"][0] != "gu`yuan`yan" || item.Alias["en"] != nil {
		t.Errorf("item: %+v", item)
	}
	if it := c.Items["nokeywords"]; it.Alias != nil || it.Pron != nil {
		t.Errorf("no keywords: %+v", it)
	}
	if it := c.Items["malformed"]; it.Name.In("en") != "Still Named" || len(it.Alias["zh"]) != 0 {
		t.Errorf("malformed keywords: %+v", it)
	}
	if stage := c.Stages["main_01-07"]; stage.IsGacha || stage.Zone != c.Zones["main_1"] {
		t.Errorf("stage: %+v", stage)
	}
	if gacha := c.Stages["gacha_1"]; !gacha.IsGacha {
		t.Errorf("gacha: %+v", gacha)
	}
	zone := c.Zones["act52side_zone1"]
	if zone.Existence["CN"].CloseTime.Int64 != 1792699200000 || zone.Existence["JP"].CloseTime.Valid {
		t.Errorf("zone: %+v", zone)
	}
	if !zone.Existence.OpenAt("CN", time.UnixMilli(1792000000000)) || zone.Existence.OpenAt("CN", time.UnixMilli(1792699200000)) {
		t.Error("event not open between its open and close times only")
	}
}

func TestSanityPerItem(t *testing.T) {
	zone := &SEOZone{ArkID: "main_1", Category: "MAINLINE"}
	stage := func(sanity null.Int) *SEOStage { return &SEOStage{ArkID: "main_01-07", Sanity: sanity, Zone: zone} }

	// from the counts, not the rounded rate: 6 × 1061347 ÷ 1322056
	got, ok := stage(null.IntFrom(6)).SanityPerItem(1061347, 1322056)
	if want := 6 * 1061347.0 / 1322056; !ok || got != want {
		t.Errorf("SanityPerItem = %v, %v; want %v", got, ok, want)
	}
	if got, ok := stage(null.IntFrom(6)).SanityCost(); !ok || got != 6 {
		t.Errorf("SanityCost = %v, %v", got, ok)
	}
	// a rate below 5e-6 rounds to 0, but it has a sanity per item
	if got, ok := stage(null.IntFrom(10)).SanityPerItem(1000000, 1); !ok || got != 1e7 {
		t.Errorf("rare drop: %v, %v", got, ok)
	}

	for name, c := range map[string]struct {
		stage           *SEOStage
		times, quantity int
	}{
		"no stage":       {nil, 10, 1},
		"no sanity":      {stage(null.Int{}), 10, 1},
		"sanity 0":       {stage(null.IntFrom(0)), 10, 1},
		"placeholder 99": {stage(null.IntFrom(constant.DefaultNullSanity)), 10, 1},
		"gacha":          {&SEOStage{ArkID: "gacha_1", Sanity: null.IntFrom(6), IsGacha: true, Zone: zone}, 10, 1},
		"recruit":        {&SEOStage{ArkID: "recruit", Sanity: null.IntFrom(6)}, 10, 1},
		"recruit zone":   {&SEOStage{ArkID: "x", Sanity: null.IntFrom(6), Zone: &SEOZone{Category: "RECRUIT"}}, 10, 1},
		"never dropped":  {stage(null.IntFrom(6)), 10, 0},
		"no runs":        {stage(null.IntFrom(6)), 0, 0},
	} {
		if got, ok := c.stage.SanityPerItem(c.times, c.quantity); ok || got != 0 {
			t.Errorf("%s: SanityPerItem = %v, %v; want none", name, got, ok)
		}
	}
}

func TestStatsPageURLs(t *testing.T) {
	const site = "https://penguin-stats.io"
	stage := &SEOStage{ArkID: "main_01-07", Zone: &SEOZone{ArkID: "main_1"}}
	// want "": no page to link
	for name, c := range map[string]struct {
		url  func() (string, bool)
		want string
	}{
		"item":                       {func() (string, bool) { return ItemPageURL(site, "zh", "CN", "30012") }, site + "/zh/result/cn/item/30012"},
		"site with a trailing slash": {func() (string, bool) { return ItemPageURL(site+"/", "en", "US", "30012") }, site + "/en/result/us/item/30012"},
		"ID escaped":                 {func() (string, bool) { return ItemPageURL(site, "en", "CN", "a/b c") }, site + "/en/result/cn/item/a%2Fb%20c"},
		"server in lower case":       {func() (string, bool) { return ItemPageURL(site, "ja", "jp", "30012") }, site + "/ja/result/jp/item/30012"},
		"unknown server":             {func() (string, bool) { return ItemPageURL(site, "en", "TW", "30012") }, ""},
		"language in upper case":     {func() (string, bool) { return ItemPageURL(site, "EN", "CN", "30012") }, ""},
		"unknown language":           {func() (string, bool) { return ItemPageURL(site, "fr", "CN", "30012") }, ""},
		"empty item ID":              {func() (string, bool) { return ItemPageURL(site, "en", "CN", "") }, ""},
		"item ID a dot segment":      {func() (string, bool) { return ItemPageURL(site, "en", "CN", "..") }, ""},
		"item ID ending in .md":      {func() (string, bool) { return ItemPageURL(site, "en", "CN", "x.md") }, ""},
		"item ID .md":                {func() (string, bool) { return ItemPageURL(site, "en", "CN", ".md") }, site + "/en/result/cn/item/.md"},
		"stage":                      {func() (string, bool) { return StagePageURL(site, "ko", "KR", stage) }, site + "/ko/result/kr/stage/main_1/main_01-07"},
		"stage without a zone":       {func() (string, bool) { return StagePageURL(site, "en", "CN", &SEOStage{ArkID: "orphan"}) }, ""},
		"no stage":                   {func() (string, bool) { return StagePageURL(site, "en", "CN", nil) }, ""},
		"zone ID a dot segment": {func() (string, bool) {
			return StagePageURL(site, "en", "CN", &SEOStage{ArkID: "x", Zone: &SEOZone{ArkID: "."}})
		}, ""},
		"empty zone ID": {func() (string, bool) { return StagePageURL(site, "en", "CN", &SEOStage{ArkID: "x", Zone: &SEOZone{}}) }, ""},
		"stage ID ending in .md": {func() (string, bool) {
			return StagePageURL(site, "en", "CN", &SEOStage{ArkID: "x.md", Zone: &SEOZone{ArkID: "main_1"}})
		}, ""},
		"zone ID ending in .md": {func() (string, bool) {
			return StagePageURL(site, "en", "CN", &SEOStage{ArkID: "x", Zone: &SEOZone{ArkID: "z.md"}})
		}, site + "/en/result/cn/stage/z.md/x"},
		"stage on no server": {func() (string, bool) { return StagePageURL(site, "en", "", stage) }, ""},
	} {
		u, ok := c.url()
		if u != c.want || ok != (c.want != "") {
			t.Errorf("%s: %q, %v; want %q", name, u, ok, c.want)
		}
	}
	if got := StatsPagePath("en", "CN", ""); got != "/en/result/cn" {
		t.Errorf("StatsPagePath without a section: %q", got)
	}
	if got := StatsPagePath("en", "CN", StatsSectionStage); got != "/en/result/cn/stage" {
		t.Errorf("StatsPagePath of the stage list: %q", got)
	}
}

func TestStatsDisplayNames(t *testing.T) {
	zone := &SEOZone{ArkID: "main_1", Name: SEOI18n{"zh": "黑暗时代·上", "en": "Evil Time Part 1"}}
	stage := &SEOStage{ArkID: "main_01-07", Code: SEOI18n{"zh": "1-7"}, Zone: zone}
	recruit := &SEOStage{ArkID: "recruit", Code: SEOI18n{"en": "Recruit"}, Zone: &SEOZone{ArkID: "recruit", Name: SEOI18n{"en": "Recruit"}}}
	for i, c := range []struct{ got, want string }{
		// the frontend's fallback, then the ID
		{(&SEOItem{ArkID: "30012", Name: SEOI18n{"en": "Orirock Cube"}}).DisplayName("ja"), "Orirock Cube"},
		{(&SEOItem{ArkID: "30012"}).DisplayName("en"), "30012"},
		{zone.DisplayName("ko"), "Evil Time Part 1"},
		{(&SEOZone{ArkID: "main_2"}).DisplayName("en"), "main_2"},
		{(*SEOZone)(nil).DisplayName("en"), ""},
		{stage.DisplayCode("en"), "1-7"},
		{(&SEOStage{ArkID: "x"}).DisplayCode("en"), "x"},
		// the code and the zone, the zone once if named like the stage, none without
		{stage.DisplayName("zh"), "1-7 黑暗时代·上"},
		{recruit.DisplayName("en"), "Recruit"},
		{(&SEOStage{ArkID: "orphan", Code: SEOI18n{"en": "X"}}).DisplayName("en"), "X"},
	} {
		if c.got != c.want {
			t.Errorf("%d: %q, want %q", i, c.got, c.want)
		}
	}
}

func TestStatsPagesOnServer(t *testing.T) {
	c := NewSEOCatalog(
		[]*model.Item{
			{ArkItemID: "30012", Type: "MATERIAL", Existence: []byte(`{"CN":{"exist":true},"US":{"exist":false}}`)},
			{ArkItemID: "tag_1", Type: "RECRUIT_TAG", Existence: []byte(`{"CN":{"exist":true}}`)},
		},
		[]*model.Stage{
			{ArkStageID: "main_01-07", StageID: 1, ZoneID: 1, Existence: []byte(`{"CN":{"exist":true},"US":{"exist":false}}`)},
			{ArkStageID: "orphan", StageID: 2, ZoneID: 99, Existence: []byte(`{"CN":{"exist":true}}`)},
		},
		[]*model.Zone{{ArkZoneID: "main_1", ZoneID: 1, Category: "MAINLINE"}},
	)
	us := NewSEOMatrix("US", nil, time.Time{})
	if !ItemOnServer(c.Items["30012"], "CN", nil) || ItemOnServer(c.Items["30012"], "US", us) || ItemOnServer(c.Items["tag_1"], "CN", nil) || ItemOnServer(nil, "CN", nil) {
		t.Error("items on a server by existence")
	}
	if !StageOnServer(c.Stages["main_01-07"], "CN", nil) || StageOnServer(c.Stages["main_01-07"], "US", us) || StageOnServer(c.Stages["orphan"], "CN", nil) {
		t.Error("stages on a server by existence")
	}
	// not existing there, but with drops there
	us = NewSEOMatrix("US", []*modelv2.OneDropMatrixElement{{StageID: "main_01-07", ItemID: "30012", Times: 10, Quantity: 1}}, time.Time{})
	if !ItemOnServer(c.Items["30012"], "US", us) || !StageOnServer(c.Stages["main_01-07"], "US", us) {
		t.Error("items and stages with drops on a server")
	}
	if !IsListedItemType("ACTIVITY_ITEM") || IsListedItemType("RECRUIT_TAG") || IsListedItemType("ARKPLANNER") {
		t.Error("listed item types")
	}
	if !IsRecruitStage(&SEOStage{ArkID: "recruit"}) || IsRecruitStage(c.Stages["main_01-07"]) {
		t.Error("recruitment statistics")
	}
}
