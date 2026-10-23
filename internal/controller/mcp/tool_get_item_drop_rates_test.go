package mcp

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/service"
)

// getItemDropRates calls get_item_drop_rates with args, and returns its result, which must not be an error.
func getItemDropRates(t *testing.T, app *fiber.App, args string) (itemDropRates, string) {
	t.Helper()
	res := callTool(t, app, "get_item_drop_rates", args)
	if res.IsError {
		t.Fatalf("%s: error: %s", args, res.text(t))
	}
	var out itemDropRates
	if err := json.Unmarshal(res.StructuredContent, &out); err != nil {
		t.Fatal(err)
	}
	return out, res.text(t)
}

// stageIDs are the stages of the rows.
func stageIDs(rows []stageRate) []string {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.StageID)
	}
	return ids
}

// Where to farm Orirock Cube on CN on 2026-10-22: 1-7 first, as on the frontend sorted by sanity per item.
func TestGetItemDropRates(t *testing.T) {
	_, app := testServer(&fakeData{catalog: testCatalog(), matrices: testMatrices()})
	out, text := getItemDropRates(t, app, `{"server":"CN","item_id":"30012"}`)
	// the stages open now (not BD-5), by sanity per item; then the rows of few runs, then those without a sanity cost
	want := []string{"main_01-07", "sub_02-12", "act19side_05_perm", "act9d0_05_perm", "act11d0_01_perm", "act52side_04",
		"main_10-01", "tough_10-01", "act18d3_01_perm", "randomMaterial_1"}
	if got := stageIDs(out.Rows); !slices.Equal(got, want) {
		t.Errorf("rows %q, want %q", got, want)
	}
	if out.Server != "CN" || out.DataAsOf != "2026-10-22T16:32:01Z" || out.IncludeClosed || out.SortBy != "sanity_per_item" ||
		out.TotalStages != 10 || out.Returned != 10 || out.Truncated || out.Definitions != dropDefinitions {
		t.Errorf("result %+v", out)
	}
	if out.Item != (itemRef{ItemID: "30012", Name: "Orirock Cube", Type: "MATERIAL", PageURL: out.Item.PageURL}) ||
		*out.Item.PageURL != "https://penguin-stats.io/en/result/cn/item/30012" || out.Source.URL != *out.Item.PageURL {
		t.Errorf("item %+v, source %+v", out.Item, out.Source)
	}
	first := `{"stageId":"main_01-07","code":"1-7","zoneName":"Episode 1","apCost":6,"times":2168783984,"quantity":2700151376,` +
		`"dropRate":1.245,"sanityPerItem":4.819,"start":"2019-05-01T02:00:00Z","end":null,"openNow":true,"dropsNow":true,"lowSample":false,` +
		`"pageUrl":"https://penguin-stats.io/en/result/cn/stage/main_1/main_01-07"}`
	if got := string(mustMarshal(t, out.Rows[0])); got != first {
		t.Errorf("first row\n%s\nwant\n%s", got, first)
	}
	for i, r := range out.Rows {
		if !r.OpenNow || !r.DropsNow || r.LowSample != (r.StageID == "act18d3_01_perm") {
			t.Errorf("row %d: %+v", i, r)
		}
	}
	if gacha := out.Rows[9]; gacha.APCost != nil || gacha.SanityPerItem != nil || gacha.DropRate != 0.07044 {
		t.Errorf("gacha %+v", gacha)
	}
	if out.Note != "Rates of fewer than 100 runs (lowSample) are noisy: they rank after the others by sanity per item." {
		t.Errorf("note %q", out.Note)
	}

	// the text has the numbers, the server, the time of the data and the credit
	for _, s := range []string{
		"Drop rates of Orirock Cube (30012) on CN, data as of 2026-10-22T16:32:01Z. 10 stages open now drop it, lowest sanity per item first",
		"| stageId | code | zone | sanity | times | quantity | drop rate | sanity per item | window (UTC) |",
		"| main_01-07 | 1-7 | Episode 1 | 6 | 2168783984 | 2700151376 | 1.245 | 4.819 | 2019-05-01 – now |",
		"| act52side_04 | YW-4 | 昨日海 | 12 | 6154517 | 10259729 | 1.667 | 7.198 | 2026-10-09 – now |",
		"| act18d3_01_perm | SV-1 | Under Tides - Intermezzi | 12 | 40 (few runs) | 100 | 2.5 | 4.8 | 2021-05-21 – now |",
		"| randomMaterial_1 | Rhodes Island Supplies | Supplies | - | 19918 | 1403 | 0.07044 | - | 2019-12-24 – now |",
		"drop rate = quantity ÷ times",
		"Data: Penguin Statistics (https://penguin-stats.io), CC BY-NC 4.0 (https://creativecommons.org/licenses/by-nc/4.0/). Source: https://penguin-stats.io/en/result/cn/item/30012",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("text lacks %q:\n%s", s, text)
		}
	}
	if strings.Contains(text, `"stageId"`) || strings.Contains(text, "drops now |") {
		t.Errorf("text holds the JSON or a drops now column:\n%s", text)
	}
}

func TestGetItemDropRatesOptions(t *testing.T) {
	_, app := testServer(&fakeData{catalog: testCatalog(), matrices: testMatrices()})
	for _, tc := range []struct {
		args string
		want []string
		// total stages
		total int
	}{
		{`{"server":"CN","item_id":"30012","top_n":3}`, []string{"main_01-07", "sub_02-12", "act19side_05_perm"}, 10},
		// the page order: rate, then times; few runs are not set apart
		{`{"server":"CN","item_id":"30012","sort_by":"drop_rate"}`, []string{"act18d3_01_perm", "sub_02-12", "act19side_05_perm", "act9d0_05_perm",
			"act52side_04", "act11d0_01_perm", "main_01-07", "main_10-01", "tough_10-01", "randomMaterial_1"}, 10},
		// the past event BD-5, by sanity per item second
		{`{"server":"CN","item_id":"30012","include_closed":true,"top_n":3}`, []string{"main_01-07", "act21mini_05", "sub_02-12"}, 11},
		// Orirock on 14-3, open, before its drop table changed
		{`{"server":"CN","item_id":"30011"}`, []string{"main_01-07", "sub_02-12"}, 2},
		{`{"server":"CN","item_id":"30011","include_closed":true}`, []string{"main_14-02", "main_01-07", "sub_02-12"}, 3},
	} {
		out, _ := getItemDropRates(t, app, tc.args)
		if got := stageIDs(out.Rows); !slices.Equal(got, tc.want) {
			t.Errorf("%s: rows %q, want %q", tc.args, got, tc.want)
		}
		if out.TotalStages != tc.total || out.Returned != len(tc.want) || out.Truncated != (len(tc.want) < tc.total) {
			t.Errorf("%s: total %d, returned %d, truncated %v", tc.args, out.TotalStages, out.Returned, out.Truncated)
		}
	}

	out, _ := getItemDropRates(t, app, `{"server":"CN","item_id":"30012","sort_by":"drop_rate"}`)
	if out.Note != "Rates of fewer than 100 runs (lowSample) are noisy." || !out.Rows[0].LowSample {
		t.Errorf("note %q, first row %+v", out.Note, out.Rows[0])
	}
	out, text := getItemDropRates(t, app, `{"server":"CN","item_id":"30012","include_closed":true,"top_n":3,"lang":"zh"}`)
	if r := out.Rows[1]; r.OpenNow || r.DropsNow || r.End == nil || *r.End != "2026-07-16T20:00:00Z" || r.Start != "2026-07-10T04:00:00Z" || r.ZoneName != "丛林症结" ||
		*r.PageURL != "https://penguin-stats.io/zh/result/cn/stage/act21mini_zone1/act21mini_05" {
		t.Errorf("past event %+v", r)
	}
	for _, s := range []string{
		"11 stages dropped it, open now or not; the first 3, lowest sanity per item first",
		"Raise top_n (at most 100) for more.",
		"| window (UTC) | drops now |",
		"| act21mini_05 | BD-5 | 丛林症结 | 15 | 188240 | 432192 | 2.296 | 6.533 | 2026-07-10 – 2026-07-16 | no |",
		"Source: https://penguin-stats.io/zh/result/cn/item/30012",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("text lacks %q:\n%s", s, text)
		}
	}
	out, _ = getItemDropRates(t, app, `{"server":"CN","item_id":"30011","include_closed":true}`)
	// 14-3 is open, but no longer drops it
	if r := out.Rows[0]; !r.OpenNow || r.DropsNow || *r.End != "2024-05-14T20:00:00Z" {
		t.Errorf("ended row %+v", r)
	}

	// once the event closed
	srv, _ := newServer(&tools{data: &fakeData{catalog: testCatalog(), matrices: testMatrices()}, site: site, clock: func() time.Time {
		return time.Date(2026, 10, 22, 20, 0, 0, 0, time.UTC)
	}}, "test")
	app = fiber.New()
	Mount(app, srv, 1000, time.Minute)
	if out, _ := getItemDropRates(t, app, `{"server":"CN","item_id":"30012"}`); slices.Contains(stageIDs(out.Rows), "act52side_04") || out.TotalStages != 9 {
		t.Errorf("closed: rows %q", stageIDs(out.Rows))
	}
}

// Models pass the names they are given: one item's name or alias is as good as its ID, several items' is an error that
// lists them.
func TestGetItemDropRatesByName(t *testing.T) {
	_, app := testServer(&fakeData{catalog: testCatalog(), matrices: testMatrices()})
	for _, id := range []string{"固源岩", "Orirock Cube", "ORIROCK-CUBE", "初級源岩", "guyuanyan", "shokyuugengan"} {
		if out, _ := getItemDropRates(t, app, `{"server":"CN","item_id":`+strconv.Quote(id)+`}`); out.Item.ItemID != "30012" {
			t.Errorf("%s: item %+v", id, out.Item)
		}
	}
	// an ID given as a number, as models do
	if out, _ := getItemDropRates(t, app, `{"server":"CN","item_id":30012}`); out.Item.ItemID != "30012" {
		t.Errorf("30012 as a number: item %+v", out.Item)
	}
	for _, tc := range []struct{ id, want string }{
		{"石头", `item_id "石头" is the name or alias of 4 items: 30011 (Orirock); 30012 (Orirock Cube); 30013 (Orirock Cluster); ` +
			`30014 (Orirock Concentration). Ask the user which one is meant, or pick by context, and pass its item ID`},
		{"Carbon Brick", `item_id "Carbon Brick" is the name or alias of 2 items: 3113 (Carbon Brick); 3114 (Carbon Brick).`},
		{"orirock c", `unknown item_id "orirock c": no item has this ID, name or alias, but 3 items match it in part: ` +
			`30012 (Orirock Cube); 30013 (Orirock Cluster); 30014 (Orirock Concentration). Pass the item ID`},
		{"Orundum", `unknown item_id "Orundum": no item has this ID, name or alias. Find the item with search_items`},
		{"・", `unknown item_id "・": no item has this ID, name or alias.`},
	} {
		res := callTool(t, app, "get_item_drop_rates", `{"server":"CN","item_id":`+strconv.Quote(tc.id)+`}`)
		if text := res.text(t); !res.IsError || !strings.Contains(text, tc.want) {
			t.Errorf("%s: error %v: %s", tc.id, res.IsError, text)
		}
	}
	// the names in the language asked for
	res := callTool(t, app, "get_item_drop_rates", `{"server":"CN","item_id":"石头","lang":"zh"}`)
	if text := res.text(t); !strings.Contains(text, "30011 (源岩); 30012 (固源岩)") {
		t.Errorf("text %s", text)
	}

	// the items of the types not listed, when no listed item has the name: with a note on what they are
	out, _ := getItemDropRates(t, app, `{"server":"CN","item_id":"D32 Steel"}`)
	if out.Item.ItemID != "30135" || len(out.Rows) != 0 || !strings.Contains(out.Note, "crafted in the workshop") ||
		!strings.Contains(out.Note, "No drop of D32 Steel is recorded on CN") {
		t.Errorf("D32 Steel: %+v", out)
	}
	out, _ = getItemDropRates(t, app, `{"server":"CN","item_id":"资深干员"}`)
	if out.Item.ItemID != "recruit_tag_senior_operator" || len(out.Rows) != 1 || !strings.Contains(out.Note, "recruitment tags") {
		t.Errorf("资深干员: %+v", out)
	}
	if out, _ := getItemDropRates(t, app, `{"server":"CN","item_id":"绿票"}`); out.Item.ItemID != "4005" {
		t.Errorf("绿票: %+v", out.Item)
	}
}

// Items of the types the frontend does not list have rows too (the tags of the recruitment), or none.
func TestGetItemDropRatesWithoutPage(t *testing.T) {
	_, app := testServer(&fakeData{catalog: testCatalog(), matrices: testMatrices()})
	out, text := getItemDropRates(t, app, `{"server":"CN","item_id":"recruit_tag_senior_operator"}`)
	if len(out.Rows) != 1 || out.Item.PageURL != nil || out.Source.URL != site {
		t.Fatalf("result %+v", out)
	}
	if r := out.Rows[0]; r.StageID != "recruit" || r.APCost != nil || r.SanityPerItem != nil || r.DropRate != 0.001615 ||
		*r.PageURL != "https://penguin-stats.io/en/result/cn/stage/recruit/recruit" {
		t.Errorf("row %+v", r)
	}
	if !strings.Contains(text, "1 stage open now drops it.") || strings.Contains(text, "first") {
		t.Errorf("text:\n%s", text)
	}

	// no drops: an empty result, not an error
	out, text = getItemDropRates(t, app, `{"server":"CN","item_id":"4005"}`)
	if len(out.Rows) != 0 || out.TotalStages != 0 || out.Item.PageURL != nil || !strings.Contains(string(mustMarshal(t, out)), `"rows":[]`) ||
		out.Note != "LGG_SHD items are not ones Penguin Statistics lists drop rates for: few stages drop them, if any. "+
			"No drop of Commendation Certificate is recorded on CN: no stage drops it there, or Penguin Statistics does not record it." {
		t.Errorf("result %+v", out)
	}
	if !strings.Contains(text, "0 stages open now drop it.") || !strings.Contains(text, out.Note) || strings.Contains(text, "| stageId |") {
		t.Errorf("text:\n%s", text)
	}
}

// An item no stage open now drops: the note says to include the closed ones.
func TestGetItemDropRatesAllClosed(t *testing.T) {
	_, app := testServer(&fakeData{catalog: testCatalog(), matrices: map[string]*service.SEOMatrix{
		"CN": service.NewSEOMatrix("CN", []*modelv2.OneDropMatrixElement{
			{StageID: "act21mini_05", ItemID: "30013", Times: 188240, Quantity: 4321, StartTime: 1783656000000, EndTime: null.IntFrom(1784232000000)},
			// a stage the catalog does not have
			{StageID: "act99side_01", ItemID: "30013", Times: 100, Quantity: 10, StartTime: 1783656000000},
		}, lastModified),
	}})
	out, text := getItemDropRates(t, app, `{"server":"CN","item_id":"30013"}`)
	if len(out.Rows) != 0 || out.Note != "No stage open on CN now drops Orirock Cluster, but 2 did: set include_closed to true to see them." {
		t.Errorf("result %+v", out)
	}
	if !strings.Contains(text, out.Note) {
		t.Errorf("text:\n%s", text)
	}
	out, _ = getItemDropRates(t, app, `{"server":"CN","item_id":"30013","include_closed":true}`)
	if got := stageIDs(out.Rows); !slices.Equal(got, []string{"act21mini_05", "act99side_01"}) {
		t.Fatalf("rows %q", got)
	}
	if r := out.Rows[1]; r.Code != "act99side_01" || r.ZoneName != "" || r.APCost != nil || r.SanityPerItem != nil || r.OpenNow || r.DropsNow ||
		r.PageURL != nil {
		t.Errorf("row of an unknown stage %+v", r)
	}
}

func TestGetItemDropRatesBadInput(t *testing.T) {
	_, app := testServer(&fakeData{catalog: testCatalog(), matrices: testMatrices()})
	for _, args := range []string{
		`{"server":"CN","item_id":""}`,
		`{"server":"CN","item_id":"30012","top_n":0}`,
		`{"server":"CN","item_id":"30012","top_n":101}`,
		`{"server":"CN","item_id":"30012","sort_by":"best"}`,
		`{"item_id":"30012","server":"TW"}`,
		`{"server":"CN","item_id":"30012","include_closed":"yes"}`,
		`{}`,
	} {
		if res := callTool(t, app, "get_item_drop_rates", args); !res.IsError {
			t.Errorf("%s: not an error: %s", args, res.StructuredContent)
		}
	}
}

func TestGetItemDropRatesWhileCold(t *testing.T) {
	data := &fakeData{}
	_, app := testServer(data)
	if res := callTool(t, app, "get_item_drop_rates", `{"server":"CN","item_id":"30012"}`); !res.IsError || res.text(t) != errCold.Error() {
		t.Errorf("no catalog: %+v", res)
	}
	// the matrix of US is not calculated yet
	data.catalog, data.matrices = testCatalog(), testMatrices()
	if res := callTool(t, app, "get_item_drop_rates", `{"item_id":"30012","server":"US"}`); !res.IsError || res.text(t) != errCold.Error() {
		t.Errorf("no matrix: %+v", res)
	}
}

// The most rows with long names stay below the budget of a result (about 25k tokens). With the data of 2026-10-22,
// the largest is of 48 KB (Orirock Cube on CN in ja, with every stage).
func TestGetItemDropRatesSize(t *testing.T) {
	// the longest stage ID, zone ID and zone name of 2026-10-22 in every row, and a code as long as those of the
	// stages with a sanity cost
	zone := "Come Catastrophes or Wakes of Vultures - Rerun"
	stages := make([]*model.Stage, 150)
	rows := make([]*modelv2.OneDropMatrixElement, len(stages))
	for i := range stages {
		id := fmt.Sprintf("randomMaterial_sies%03d", i)
		code := fmt.Sprintf("SSR-%03d", i)
		stages[i] = &model.Stage{ArkStageID: id, StageID: i, ZoneID: 1, Code: i18n(code, code, code, code), Sanity: null.IntFrom(21), Existence: existence(all...)}
		rows[i] = &modelv2.OneDropMatrixElement{StageID: id, ItemID: "30012", Times: 2168783984 - i, Quantity: 2700151376 + 7*i,
			StartTime: 1556676000000, EndTime: null.IntFrom(1784232000000)}
	}
	catalog := testCatalog()
	big := service.NewSEOCatalog(nil, stages, []*model.Zone{
		{ArkZoneID: "permanent_sidestory_10_zone1", ZoneID: 1, Category: "ACTIVITY", Name: i18n(zone, zone, zone, zone), Existence: existence(all...)},
	})
	big.Items, big.ItemList = catalog.Items, catalog.ItemList
	_, app := testServer(&fakeData{catalog: big, matrices: map[string]*service.SEOMatrix{"CN": service.NewSEOMatrix("CN", rows, lastModified)}})

	res := callTool(t, app, "get_item_drop_rates", `{"server":"CN","item_id":"30012","top_n":100,"include_closed":true}`)
	var out itemDropRates
	if err := json.Unmarshal(res.StructuredContent, &out); err != nil || len(out.Rows) != 100 || out.TotalStages != 150 || !out.Truncated {
		t.Fatalf("result %.300s", res.StructuredContent)
	}
	if n := len(res.StructuredContent) + len(res.text(t)); n > 60<<10 {
		t.Errorf("result of %d bytes", n)
	}
}
