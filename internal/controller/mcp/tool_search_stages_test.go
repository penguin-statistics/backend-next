package mcp

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/service"
)

// searchStages calls search_stages with args, and returns its result, which must not be an error.
func searchStages(t *testing.T, app *fiber.App, args string) (stageSearch, string) {
	t.Helper()
	res := callTool(t, app, "search_stages", args)
	if res.IsError {
		t.Fatalf("%s: error: %s", args, res.text(t))
	}
	var out stageSearch
	if err := json.Unmarshal(res.StructuredContent, &out); err != nil {
		t.Fatal(err)
	}
	return out, res.text(t)
}

// The collisions of the codes of the catalog of 2026-10-22.
func TestSearchStages(t *testing.T) {
	_, app := testServer(&fakeData{catalog: testCatalog()})
	for _, tc := range []struct {
		query string
		// the results: ID, matchedBy, matchType and matchedText
		want      []string
		ambiguous bool
	}{
		// the code before the codes it is in
		{"1-7", []string{"main_01-07 code exact 1-7", "main_11-06 code substring 11-7", "tough_11-06 code substring 11-7"}, false},
		// as typed in full width
		{"１－７", []string{"main_01-07 code exact 1-7", "main_11-06 code substring 11-7", "tough_11-06 code substring 11-7"}, false},
		{"1 - 7", []string{"main_01-07 code exact 1-7", "main_11-06 code substring 11-7", "tough_11-06 code substring 11-7"}, false},
		// the standard and tough versions share the code: the standard one first
		{"10-2", []string{"main_10-01 code exact 10-2", "tough_10-01 code exact 10-2"}, true},
		// two events, the permanent stage of the second and its rerun: the open one first, then the originals by ID
		{"SV-1", []string{"act18d3_01_perm code exact SV-1", "act10d5_01 code exact SV-1", "act18d3_01 code exact SV-1",
			"act18d3_01_rep code exact SV-1"}, true},
		{"sv-1", []string{"act18d3_01_perm code exact SV-1", "act10d5_01 code exact SV-1", "act18d3_01 code exact SV-1",
			"act18d3_01_rep code exact SV-1"}, true},
		{"YW-4", []string{"act52side_04 code exact YW-4"}, false},
		{"YW", []string{"act52side_04 code prefix YW-4"}, false},
		// the ID, whatever its case
		{"main_01-07", []string{"main_01-07 id exact main_01-07"}, false},
		{"MAIN_01-07", []string{"main_01-07 id exact main_01-07"}, false},
		{"recruit", []string{"recruit id exact recruit"}, false},
		// the codes of the gachas are names
		{"Rhodes Island", []string{"randomMaterial_1 code prefix Rhodes Island Supplies"}, false},
		// the zones, after the codes: all their stages, which are a list rather than a choice
		{"第十章", []string{"main_10-01 zone prefix 第十章 (标准)", "tough_10-01 zone prefix 第十章 (磨难)"}, false},
		{"第十章（磨难）", []string{"tough_10-01 zone exact 第十章 (磨难)"}, false},
		{"Under Tides", []string{"act18d3_01 zone exact Under Tides", "act18d3_01_perm zone prefix Under Tides - Intermezzi",
			"act18d3_01_rep zone prefix Under Tides - Rerun"}, false},
		{"覆潮之下复刻", []string{"act18d3_01_rep zone exact 覆潮之下・复刻"}, false},
		{"act52side_zone1", []string{"act52side_04 zone exact act52side_zone1"}, false},
		{"0-1", nil, false},
	} {
		out, _ := searchStages(t, app, `{"query":`+strconv.Quote(tc.query)+`}`)
		got := make([]string, 0, len(out.Results))
		for _, r := range out.Results {
			got = append(got, strings.Join([]string{r.StageID, r.MatchedBy, r.MatchType, r.MatchedText}, " "))
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: results %q, want %q", tc.query, got, tc.want)
		}
		if out.Ambiguous != tc.ambiguous || out.Truncated || out.Query != tc.query || out.Server != "CN" || out.Results == nil {
			t.Errorf("%s: ambiguous %v, truncated %v, query %q, server %q, results %v", tc.query, out.Ambiguous, out.Truncated,
				out.Query, out.Server, out.Results)
		}
	}
	// nor does the text ask to choose
	if _, text := searchStages(t, app, `{"query":"第十章"}`); strings.Contains(text, "match equally well") {
		t.Errorf("text of a zone:\n%s", text)
	}
}

func TestSearchStagesResult(t *testing.T) {
	_, app := testServer(&fakeData{catalog: testCatalog()})
	out, text := searchStages(t, app, `{"query":"10-2","lang":"zh"}`)
	if len(out.Results) != 2 {
		t.Fatalf("results %+v", out.Results)
	}
	sanity := int64(21)
	want := []stageMatch{
		{stageRef: stageRef{StageID: "main_10-01", Code: "10-2", ZoneID: "main_10", ZoneName: "第十章 (标准)", ZoneType: "MAINLINE",
			APCost: &sanity, OpenNow: true, ExistsOn: all, PageURL: link("https://penguin-stats.io/zh/result/cn/stage/main_10/main_10-01", true)},
			Difficulty: "normal", Variant: "original", MatchedBy: "code", MatchType: "exact", MatchedText: "10-2"},
		{stageRef: stageRef{StageID: "tough_10-01", Code: "10-2", ZoneID: "main_10_tough", ZoneName: "第十章 (磨难)", ZoneType: "MAINLINE",
			APCost: &sanity, OpenNow: true, ExistsOn: all, PageURL: link("https://penguin-stats.io/zh/result/cn/stage/main_10_tough/tough_10-01", true)},
			Difficulty: "tough", Variant: "original", MatchedBy: "code", MatchType: "exact", MatchedText: "10-2"},
	}
	for i := range want {
		if got, w := mustMarshal(t, out.Results[i]), mustMarshal(t, want[i]); string(got) != string(w) {
			t.Errorf("result %d:\n%s\nwant\n%s", i, got, w)
		}
	}
	for _, s := range []string{
		`2 stages match "10-2". The first 2 match equally well (code, exact: 10-2): ask which one is meant, or pick by context`,
		"| stageId | code | zone | kind | sanity | open on CN now | matched by |",
		"| main_10-01 | 10-2 | 第十章 (标准) | MAINLINE | 21 | yes | code, exact: 10-2 |",
		"| tough_10-01 | 10-2 | 第十章 (磨难) | MAINLINE, tough | 21 | yes | code, exact: 10-2 |",
		"Data: Penguin Statistics (https://penguin-stats.io), CC BY-NC 4.0 (https://creativecommons.org/licenses/by-nc/4.0/). Source: https://penguin-stats.io",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("text lacks %q:\n%s", s, text)
		}
	}
	if strings.Contains(text, `"stageId"`) {
		t.Errorf("text holds the JSON:\n%s", text)
	}

	// the variants, and the stages without a sanity cost
	out, text = searchStages(t, app, `{"query":"SV-1"}`)
	var kinds []string
	for _, r := range out.Results {
		kinds = append(kinds, r.ZoneType+" "+r.Variant)
	}
	if want := []string{"ACTIVITY_PERMANENT permanent", "ACTIVITY original", "ACTIVITY original", "ACTIVITY rerun"}; !slices.Equal(kinds, want) {
		t.Errorf("kinds %q, want %q", kinds, want)
	}
	for _, s := range []string{
		"| act18d3_01_perm | SV-1 | Under Tides - Intermezzi | ACTIVITY_PERMANENT, permanent | 12 | yes | code, exact: SV-1 |",
		"| act18d3_01_rep | SV-1 | Under Tides - Rerun | ACTIVITY, rerun | 12 | no | code, exact: SV-1 |",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("text lacks %q:\n%s", s, text)
		}
	}
	for _, q := range []string{"recruit", "Rhodes Island Supplies"} {
		out, text = searchStages(t, app, `{"query":"`+q+`"}`)
		if len(out.Results) != 1 || out.Results[0].APCost != nil || !strings.Contains(string(mustMarshal(t, out.Results[0])), `"apCost":null`) {
			t.Errorf("%s: results %+v", q, out.Results)
		}
		if !strings.Contains(text, " | - | yes | ") {
			t.Errorf("%s: text:\n%s", q, text)
		}
	}
	if z := out.Results[0]; z.ZoneType != "GACHABOX" || z.Code != "Rhodes Island Supplies" {
		t.Errorf("gacha %+v", z)
	}
}

// Whether a stage is open, and where its page is, depends on the server.
func TestSearchStagesOnServer(t *testing.T) {
	_, app := testServer(&fakeData{catalog: testCatalog()})
	out, text := searchStages(t, app, `{"query":"YW-4","server":"CN"}`)
	if r := out.Results[0]; !r.OpenNow || !slices.Equal(r.ExistsOn, []string{"CN"}) ||
		*r.PageURL != "https://penguin-stats.io/en/result/cn/stage/act52side_zone1/act52side_04" {
		t.Errorf("CN: %+v", r)
	}
	if !strings.Contains(text, "| act52side_04 | YW-4 | 昨日海 | ACTIVITY | 12 | yes | code, exact: YW-4 |") {
		t.Errorf("CN: text:\n%s", text)
	}
	// an event of CN only: closed on US, its page on CN
	out, text = searchStages(t, app, `{"query":"YW-4","server":"US"}`)
	if r := out.Results[0]; r.OpenNow || out.Server != "US" || *r.PageURL != "https://penguin-stats.io/en/result/cn/stage/act52side_zone1/act52side_04" {
		t.Errorf("US: %+v", r)
	}
	if !strings.Contains(text, "| open on US now |") || !strings.Contains(text, "| 12 | no | code, exact: YW-4 |") {
		t.Errorf("US: text:\n%s", text)
	}
	// the stage on the server asked for
	out, _ = searchStages(t, app, `{"query":"10-2","server":"JP"}`)
	if r := out.Results[0]; *r.PageURL != "https://penguin-stats.io/en/result/jp/stage/main_10/main_10-01" {
		t.Errorf("JP: %+v", r)
	}

	// once the event closed
	srv, _ := newServer(&tools{data: &fakeData{catalog: testCatalog()}, site: site, clock: func() time.Time {
		return time.Date(2026, 10, 22, 20, 0, 0, 0, time.UTC)
	}}, "test")
	app = fiber.New()
	Mount(app, srv, 1000, time.Minute)
	if out, _ := searchStages(t, app, `{"query":"YW-4"}`); out.Results[0].OpenNow {
		t.Errorf("closed: %+v", out.Results[0])
	}
}

// Supply and chip stages count as open every day, as their weekday rotation is not tracked: the text says so.
func TestSearchStagesWeekly(t *testing.T) {
	_, app := testServer(&fakeData{catalog: weeklyCatalog()})
	out, text := searchStages(t, app, `{"query":"CA-5"}`)
	if len(out.Results) != 1 || !out.Results[0].OpenNow {
		t.Fatalf("results %+v", out.Results)
	}
	for _, s := range []string{"| yes* |", "* " + rotationCaveat} {
		if !strings.Contains(text, s) {
			t.Errorf("text lacks %q:\n%s", s, text)
		}
	}

	_, app = testServer(&fakeData{catalog: testCatalog()})
	if _, text := searchStages(t, app, `{"query":"1-7"}`); strings.Contains(text, "*") || strings.Contains(text, rotationCaveat) {
		t.Errorf("1-7:\n%s", text)
	}
}

// A stage without a zone has no page to link.
func TestSearchStagesWithoutZone(t *testing.T) {
	_, app := testServer(&fakeData{catalog: service.NewSEOCatalog(nil, []*model.Stage{
		{ArkStageID: "lost_01", ZoneID: 404, Code: i18n("LO-1", "LO-1", "LO-1", "LO-1"), Sanity: null.IntFrom(10), Existence: existence(all...)},
	}, nil)})
	out, _ := searchStages(t, app, `{"query":"LO-1"}`)
	if len(out.Results) != 1 {
		t.Fatalf("results %+v", out.Results)
	}
	if r := out.Results[0]; r.ZoneID != "" || r.ZoneName != "" || r.ZoneType != "" || r.PageURL != nil || !r.OpenNow || *r.APCost != 10 {
		t.Errorf("result %+v", r)
	}
}

func TestSearchStagesBadInput(t *testing.T) {
	_, app := testServer(&fakeData{catalog: testCatalog()})
	for _, args := range []string{
		`{"query":"・"}`,
		`{"query":""}`,
		`{"query":"1-7","server":"TW"}`,
		`{"query":"1-7","limit":26}`,
		`{}`,
	} {
		if res := callTool(t, app, "search_stages", args); !res.IsError {
			t.Errorf("%s: not an error: %s", args, res.StructuredContent)
		}
	}
	res := callTool(t, app, "search_stages", `{"query":"・"}`)
	if text := res.text(t); !strings.Contains(text, `nothing to search for in the query "・"`) {
		t.Errorf("text %q", text)
	}
}

func TestSearchStagesWhileCold(t *testing.T) {
	_, app := testServer(&fakeData{})
	if res := callTool(t, app, "search_stages", `{"query":"1-7"}`); !res.IsError || res.text(t) != errCold.Error() {
		t.Errorf("result %+v", res)
	}
}

// The most results with the longest names stay far below the budget of a result (about 25k tokens).
func TestSearchStagesSize(t *testing.T) {
	long := strings.Repeat("潮", queryMaxLength)
	stages := make([]*model.Stage, 40)
	for i := range stages {
		c := long[:len(long)-6] + strconv.Itoa(10+i)
		stages[i] = &model.Stage{ArkStageID: "act99side_" + strconv.Itoa(i) + "_rep", StageID: i, ZoneID: 1, Code: i18n(c, c, c, c),
			Sanity: null.IntFrom(30), Existence: existence(all...)}
	}
	_, app := testServer(&fakeData{catalog: service.NewSEOCatalog(nil, stages, []*model.Zone{
		{ArkZoneID: "act99side_zone1", ZoneID: 1, Category: "ACTIVITY", Name: i18n(long, long, long, long), Existence: existence(all...)},
	})})
	res := callTool(t, app, "search_stages", `{"query":"潮","limit":25}`)
	var out stageSearch
	if err := json.Unmarshal(res.StructuredContent, &out); err != nil || len(out.Results) != 25 || !out.Truncated {
		t.Fatalf("result %s", res.StructuredContent)
	}
	if n := len(res.StructuredContent) + len(res.text(t)); n > 60<<10 {
		t.Errorf("result of %d bytes", n)
	}
}
