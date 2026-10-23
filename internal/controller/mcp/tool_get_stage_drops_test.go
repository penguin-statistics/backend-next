package mcp

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/service"
)

// getStageDrops calls get_stage_drops with args, and returns its result, which must not be an error.
func getStageDrops(t *testing.T, app *fiber.App, args string) (stageDrops, string) {
	t.Helper()
	res := callTool(t, app, "get_stage_drops", args)
	if res.IsError {
		t.Fatalf("%s: error: %s", args, res.text(t))
	}
	var out stageDrops
	if err := json.Unmarshal(res.StructuredContent, &out); err != nil {
		t.Fatal(err)
	}
	return out, res.text(t)
}

// itemIDs are the items of the rows.
func itemIDs(rows []itemRate) []string {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ItemID)
	}
	return ids
}

func TestGetStageDrops(t *testing.T) {
	_, app := testServer(&fakeData{catalog: testCatalog(), matrices: testMatrices()})
	out, text := getStageDrops(t, app, `{"server":"CN","stage_id":"main_01-07"}`)
	// by rate, the bonus drop of an event that ended last, though its rate is higher than that of Orirock
	if got, want := itemIDs(out.Rows), []string{"30012", "30011", "randomMaterial_1"}; !slices.Equal(got, want) {
		t.Errorf("rows %q, want %q", got, want)
	}
	stage := `{"stageId":"main_01-07","code":"1-7","zoneId":"main_1","zoneName":"Episode 1","zoneType":"MAINLINE","apCost":6,` +
		`"openNow":true,"existsOn":["CN","US","JP","KR"],"pageUrl":"https://penguin-stats.io/en/result/cn/stage/main_1/main_01-07"}`
	if got := string(mustMarshal(t, out.Stage)); got != stage {
		t.Errorf("stage\n%s\nwant\n%s", got, stage)
	}
	first := `{"itemId":"30012","itemName":"Orirock Cube","times":2168783984,"quantity":2700151376,"dropRate":1.245,` +
		`"sanityPerItem":4.819,"start":"2019-05-01T02:00:00Z","end":null}`
	if got := string(mustMarshal(t, out.Rows[0])); got != first {
		t.Errorf("first row\n%s\nwant\n%s", got, first)
	}
	// an item the catalog does not have is named by its ID
	if r := out.Rows[2]; r.ItemName != "randomMaterial_1" || *r.End != "2020-01-06T20:00:00Z" || *r.SanityPerItem != 49.166 {
		t.Errorf("ended row %+v", r)
	}
	if out.Server != "CN" || out.DataAsOf != "2026-10-22T16:32:01Z" || out.Truncated || out.Definitions != dropDefinitions ||
		out.Source.URL != *out.Stage.PageURL ||
		out.Note != "The last item is no longer dropped: its window ended (end is not null), as the drop table of the stage "+
			"changed or the event of a bonus drop ended." {
		t.Errorf("result %+v", out)
	}
	for _, s := range []string{
		"Drops of 1-7 Episode 1 (main_01-07) on CN, data as of 2026-10-22T16:32:01Z. Sanity 6 per run. Open now. 3 items, highest drop rate first.",
		"| itemId | item | times | quantity | drop rate | sanity per item | window (UTC) |",
		"| 30012 | Orirock Cube | 2168783984 | 2700151376 | 1.245 | 4.819 | 2019-05-01 – now |",
		"| randomMaterial_1 | randomMaterial_1 | 29655 | 3619 | 0.122 | 49.166 | 2019-12-24 – 2020-01-06 |",
		"drop rate = quantity ÷ times",
		out.Note,
		"Data: Penguin Statistics (https://penguin-stats.io), CC BY-NC 4.0 (https://creativecommons.org/licenses/by-nc/4.0/). Source: https://penguin-stats.io/en/result/cn/stage/main_1/main_01-07",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("text lacks %q:\n%s", s, text)
		}
	}
	if strings.Contains(text, `"itemId"`) {
		t.Errorf("text holds the JSON:\n%s", text)
	}
}

// Models pass the codes they are given: a code of one stage on the server is as good as its ID, a code of several is
// an error that lists them with their zones.
func TestGetStageDropsByCode(t *testing.T) {
	matrices := testMatrices()
	matrices["US"] = service.NewSEOMatrix("US", nil, lastModified)
	_, app := testServer(&fakeData{catalog: testCatalog(), matrices: matrices})
	for _, tc := range []struct{ id, server, want string }{
		{"1-7", "CN", "main_01-07"},
		{"１－７", "CN", "main_01-07"},
		{"s2-12", "CN", "sub_02-12"},
		{"MAIN_01-07", "CN", "main_01-07"},
		// on CN only: the stage anyway
		{"YW-4", "US", "act52side_04"},
	} {
		if out, _ := getStageDrops(t, app, `{"server":"CN","stage_id":`+strconv.Quote(tc.id)+`,"server":"`+tc.server+`"}`); out.Stage.StageID != tc.want {
			t.Errorf("%s on %s: stage %+v", tc.id, tc.server, out.Stage)
		}
	}
	for _, tc := range []struct{ id, want string }{
		{"10-2", `stage_id "10-2" is the code of 2 stages on CN: main_10-01 (10-2 Episode 10 (Standard), open now); ` +
			`tough_10-01 (10-2 Episode 10 (Tough), open now). Ask the user which one is meant, or pick by context, and pass its stage ID`},
		// the stage open now first
		{"SV-1", `stage_id "SV-1" is the code of 4 stages on CN: act18d3_01_perm (SV-1 Under Tides - Intermezzi, open now); ` +
			`act10d5_01 (SV-1 Children of Ursus); act18d3_01 (SV-1 Under Tides); act18d3_01_rep (SV-1 Under Tides - Rerun).`},
		{"第十章", `unknown stage_id "第十章": no stage has this ID or code, but 2 stages match it in part (by code or zone): ` +
			`main_10-01 (10-2 Episode 10 (Standard), open now); tough_10-01 (10-2 Episode 10 (Tough), open now). Pass the stage ID`},
		{"0-1", `unknown stage_id "0-1": no stage has this ID or code. Find the stage with search_stages`},
		{"・", `unknown stage_id "・": no stage has this ID or code.`},
	} {
		res := callTool(t, app, "get_stage_drops", `{"server":"CN","stage_id":`+strconv.Quote(tc.id)+`}`)
		if text := res.text(t); !res.IsError || !strings.Contains(text, tc.want) {
			t.Errorf("%s: error %v: %s", tc.id, res.IsError, text)
		}
	}
}

// Codes shared by stages none of which is on the server: an error that lists them all.
func TestGetStageDropsByCodeOnNoServer(t *testing.T) {
	c := testCatalog()
	for _, id := range []string{"act10d5_01", "act18d3_01", "act18d3_01_perm", "act18d3_01_rep"} {
		c.Stages[id].Existence = service.SEOExistence{"CN": c.Stages[id].Existence["CN"]}
	}
	_, app := testServer(&fakeData{catalog: c, matrices: testMatrices()})
	res := callTool(t, app, "get_stage_drops", `{"stage_id":"SV-1","server":"KR","lang":"zh"}`)
	if text := res.text(t); !res.IsError || !strings.Contains(text, `stage_id "SV-1" is the code of 4 stages, none of them on KR: `+
		`act10d5_01 (SV-1 乌萨斯的孩子们); act18d3_01 (SV-1 覆潮之下); act18d3_01_perm (SV-1 覆潮之下・插曲); act18d3_01_rep (SV-1 覆潮之下・复刻).`) {
		t.Errorf("error %v: %s", res.IsError, text)
	}
}

// What the rows of a gacha, the recruitment, a closed stage and a stage of another server are.
func TestGetStageDropsNotes(t *testing.T) {
	matrices := testMatrices()
	matrices["JP"] = service.NewSEOMatrix("JP", nil, lastModified)
	_, app := testServer(&fakeData{catalog: testCatalog(), matrices: matrices})
	out, text := getStageDrops(t, app, `{"server":"CN","stage_id":"recruit"}`)
	if r := out.Rows[0]; len(out.Rows) != 1 || out.Stage.APCost != nil || out.Stage.ZoneType != "RECRUIT" || r.SanityPerItem != nil ||
		r.ItemName != "Senior Operator" || !strings.HasPrefix(out.Note, "This is the recruitment: times count recruitments") {
		t.Errorf("recruitment %+v", out)
	}
	if !strings.Contains(text, "Drops of Recruit (recruit) on CN, data as of 2026-10-22T16:32:01Z. Open now. 1 item, highest drop rate first.") ||
		!strings.Contains(text, "| recruit_tag_senior_operator | Senior Operator | 1224363319 | 1977025 | 0.001615 | - | 2022-08-15 – now |") {
		t.Errorf("recruitment text:\n%s", text)
	}

	out, _ = getStageDrops(t, app, `{"server":"CN","stage_id":"randomMaterial_1"}`)
	if len(out.Rows) != 1 || out.Stage.APCost != nil || out.Rows[0].SanityPerItem != nil || out.Rows[0].DropRate != 0.07044 ||
		out.Note != "The stage is a gacha: times count the items drawn, so a drop rate is per draw, and it has no sanity cost." {
		t.Errorf("gacha %+v", out)
	}

	out, text = getStageDrops(t, app, `{"server":"CN","stage_id":"BD-5","lang":"ja"}`)
	if out.Stage.StageID != "act21mini_05" || out.Stage.OpenNow || len(out.Rows) != 1 ||
		out.Note != "The stage is not open on CN now: these are the drops of when it was." {
		t.Errorf("closed stage %+v", out)
	}
	if !strings.Contains(text, "Drops of BD-5 丛林症结 (act21mini_05) on CN, data as of 2026-10-22T16:32:01Z. Sanity 15 per run. Not open now.") ||
		!strings.Contains(text, "| 30012 | 初級源岩 | 188240 | 432192 | 2.296 | 6.533 | 2026-07-10 – 2026-07-16 |") {
		t.Errorf("closed stage text:\n%s", text)
	}

	// no drops: an empty result, not an error
	out, text = getStageDrops(t, app, `{"server":"CN","stage_id":"act10d5_01"}`)
	if len(out.Rows) != 0 || !strings.Contains(string(mustMarshal(t, out)), `"rows":[]`) || out.Note != "No drop of SV-1 Children of Ursus is recorded on CN." {
		t.Errorf("no drops %+v", out)
	}
	if strings.Contains(text, "| itemId |") || !strings.Contains(text, "Not open now.\n\nNo drop of SV-1 Children of Ursus is recorded on CN.") {
		t.Errorf("no drops text:\n%s", text)
	}
	// on CN only
	out, _ = getStageDrops(t, app, `{"stage_id":"act52side_04","server":"JP"}`)
	if out.Server != "JP" || out.Stage.OpenNow || !slices.Equal(out.Stage.ExistsOn, []string{"CN"}) ||
		*out.Stage.PageURL != "https://penguin-stats.io/en/result/cn/stage/act52side_zone1/act52side_04" ||
		out.Note != "No drop of YW-4 昨日海 is recorded on JP. The stage does not exist on JP: ask for it on CN." {
		t.Errorf("stage of CN on JP %+v", out)
	}
}

func TestGetStageDropsBadInput(t *testing.T) {
	_, app := testServer(&fakeData{catalog: testCatalog(), matrices: testMatrices()})
	for _, args := range []string{
		`{"server":"CN","stage_id":""}`,
		`{"stage_id":"main_01-07","server":"TW"}`,
		`{"server":"CN","stage_id":"main_01-07","lang":"fr"}`,
		`{}`,
	} {
		if res := callTool(t, app, "get_stage_drops", args); !res.IsError {
			t.Errorf("%s: not an error: %s", args, res.StructuredContent)
		}
	}
}

func TestGetStageDropsWhileCold(t *testing.T) {
	data := &fakeData{}
	_, app := testServer(data)
	if res := callTool(t, app, "get_stage_drops", `{"server":"CN","stage_id":"main_01-07"}`); !res.IsError || res.text(t) != errCold.Error() {
		t.Errorf("no catalog: %+v", res)
	}
	data.catalog, data.matrices = testCatalog(), testMatrices()
	if res := callTool(t, app, "get_stage_drops", `{"stage_id":"main_01-07","server":"KR"}`); !res.IsError || res.text(t) != errCold.Error() {
		t.Errorf("no matrix: %+v", res)
	}
}

// A stage of more items than any has is cut at 100, below the budget of a result (about 25k tokens). The recruitment
// has the most on 2026-10-22, 29, of 10 KB.
func TestGetStageDropsSize(t *testing.T) {
	name := "Scharzhof Coating Device Starter Pack"
	items := make([]*model.Item, 150)
	rows := make([]*modelv2.OneDropMatrixElement, len(items))
	for i := range items {
		id := "recruit_tag_" + strconv.Itoa(1000+i)
		items[i] = &model.Item{ArkItemID: id, Type: "MATERIAL", Name: i18n(name, name, name, name), Existence: existence(all...)}
		rows[i] = &modelv2.OneDropMatrixElement{StageID: "main_01-07", ItemID: id, Times: 2168783984, Quantity: 2700151376 - i,
			StartTime: 1556676000000, EndTime: null.IntFrom(1784232000000)}
	}
	_, app := testServer(&fakeData{catalog: service.NewSEOCatalog(items, []*model.Stage{
		{ArkStageID: "main_01-07", ZoneID: 1, Code: i18n("1-7", "1-7", "1-7", "1-7"), Sanity: null.IntFrom(6), Existence: existence(all...)},
	}, []*model.Zone{
		{ArkZoneID: "main_1", ZoneID: 1, Category: "MAINLINE", Name: i18n("第一章", "Episode 1", "第一章", "에피소드 1"), Existence: existence(all...)},
	}), matrices: map[string]*service.SEOMatrix{"CN": service.NewSEOMatrix("CN", rows, lastModified)}})

	res := callTool(t, app, "get_stage_drops", `{"server":"CN","stage_id":"main_01-07"}`)
	var out stageDrops
	if err := json.Unmarshal(res.StructuredContent, &out); err != nil || len(out.Rows) != 100 || !out.Truncated {
		t.Fatalf("result %.300s", res.StructuredContent)
	}
	if n := len(res.StructuredContent) + len(res.text(t)); n > 60<<10 {
		t.Errorf("result of %d bytes", n)
	}
}
