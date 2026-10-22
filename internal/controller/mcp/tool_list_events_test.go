package mcp

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/service"
)

const day = 24 * time.Hour

// eventWindow is when an event is open on a server
type eventWindow struct{ open, close time.Time }

// eventExistence is the existence of an event open in the windows of the servers given, absent from the others
func eventExistence(windows map[string]eventWindow) []byte {
	m := map[string]map[string]any{}
	for _, s := range all {
		m[s] = map[string]any{"exist": false}
	}
	for s, w := range windows {
		m[s] = map[string]any{"exist": true, "openTime": w.open.UnixMilli(), "closeTime": w.close.UnixMilli()}
	}
	b, _ := json.Marshal(m)
	return b
}

// eventsCatalog is a catalog of events around now: on CN one open, one upcoming and four closed (two reruns), on
// the global servers four closed; and zones that are not events.
func eventsCatalog(now time.Time) *service.SEOCatalog {
	global := func(w eventWindow) map[string]eventWindow { return map[string]eventWindow{"US": w, "JP": w, "KR": w} }
	with := func(m map[string]eventWindow, server string, w eventWindow) map[string]eventWindow {
		m[server] = w
		return m
	}
	zones := []*model.Zone{
		{ArkZoneID: "main_1", ZoneID: 1, Index: 1, Category: "MAINLINE", Name: i18n("黑暗时代·上", "Evil Time Part 1", "暗黒時代・上", "암흑시대・상"), Existence: existence(all...)},
		{ArkZoneID: "permanent_sidestory_1_zone1", ZoneID: 2, Index: 0, Category: "ACTIVITY_PERMANENT", Type: null.StringFrom("SIDESTORY"),
			Name: i18n("覆潮之下・别传", "Under Tides", "潮汐の下・サイドストーリー", "조수의 아래 사이드 스토리"), Existence: existence(all...)},
		{ArkZoneID: "act52side_zone1", ZoneID: 3, Index: 0, Category: "ACTIVITY", Name: i18n("昨日海", "昨日海", "昨日海", "昨日海"),
			Existence: eventExistence(map[string]eventWindow{"CN": {now.Add(-13 * day), now.Add(3 * time.Hour)}})},
		{ArkZoneID: "act53side_zone1", ZoneID: 4, Index: 0, Category: "ACTIVITY", Name: i18n("直到大地变成一颗酸橙", "Until the Earth Turns Lime", "大地がライムになるまで", "대지가 라임이 될 때까지"),
			Existence: eventExistence(map[string]eventWindow{"CN": {now.Add(2 * day), now.Add(16 * day)}})},
		{ArkZoneID: "act11sre_zone1", ZoneID: 5, Index: 0, Category: "ACTIVITY", Name: i18n("覆潮之下・复刻", "Under Tides - Rerun", "潮汐の下・復刻", "조수의 아래 복각"),
			Existence: eventExistence(with(global(eventWindow{now.Add(-30 * day), now.Add(-20 * day)}), "CN", eventWindow{now.Add(-60 * day), now.Add(-50 * day)}))},
		{ArkZoneID: "act13d2_zone1", ZoneID: 6, Index: 0, Category: "ACTIVITY", Name: i18n("骑兵与猎人・复刻", "The Knight & The Hunters - Rerun", "騎兵と狩人・復刻", "기병과 사냥꾼 복각"),
			Existence: eventExistence(with(global(eventWindow{now.Add(-200 * day), now.Add(-190 * day)}), "CN", eventWindow{now.Add(-100 * day), now.Add(-90 * day)}))},
		{ArkZoneID: "act45side_zone1", ZoneID: 7, Index: 0, Category: "ACTIVITY", Name: i18n("梦中缓行", "Somniloquium Serenum", "夢中緩行", "몽중완행"),
			Existence: eventExistence(with(global(eventWindow{now.Add(-250 * day), now.Add(-240 * day)}), "CN", eventWindow{now.Add(-400 * day), now.Add(-390 * day)}))},
		{ArkZoneID: "act10d5_zone1", ZoneID: 8, Index: 0, Category: "ACTIVITY", Name: i18n("乌萨斯的孩子们", "Children of Ursus", "ウルサスの子供たち", "우르수스의 아이들"),
			Existence: eventExistence(map[string]eventWindow{"CN": {now.Add(-500 * day), now.Add(-490 * day)}})},
	}
	n := 0
	stage := func(id string, zone int, code string, servers ...string) *model.Stage {
		n++
		return &model.Stage{ArkStageID: id, StageID: n, ZoneID: zone, StageType: "ACTIVITY", Code: i18n(code, code, code, code),
			Sanity: null.IntFrom(12), Existence: existence(servers...)}
	}
	gacha := stage("act52side_gacha", 3, "炼金无限池", "CN")
	gacha.ExtraProcessType = null.StringFrom(constant.ExtraProcessTypeGachaBox)
	stages := []*model.Stage{
		stage("main_01-07", 1, "1-7", all...),
		stage("act18d3_01_perm", 2, "SV-1", all...),
		stage("act52side_01", 3, "YW-1", "CN"),
		stage("act52side_02", 3, "YW-2", "CN"),
		stage("act52side_04", 3, "YW-4", "CN"),
		gacha,
		stage("act53side_01", 4, "LE-1", "CN"),
		stage("act18d3_01_rep", 5, "SV-1", all...),
		stage("act18d3_02_rep", 5, "SV-2", "CN"),
		stage("act13d2_01_rep", 6, "OD-1", all...),
		stage("act45side_01", 7, "SS-1", all...),
		stage("act10d5_01", 8, "SV-1", "CN"),
	}
	return service.NewSEOCatalog(nil, stages, zones)
}

// listEvents calls list_events with args, and returns its result
func listEvents(t *testing.T, data Data, args string) (eventList, string) {
	t.Helper()
	_, app := testServer(data)
	res := callTool(t, app, "list_events", args)
	if res.IsError {
		t.Fatalf("error: %s", res.text(t))
	}
	var out eventList
	if err := json.Unmarshal(res.StructuredContent, &out); err != nil {
		t.Fatal(err)
	}
	return out, res.text(t)
}

func zoneIDs(events []eventEntry) []string {
	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ZoneID
	}
	return ids
}

func ms(t time.Time) *string {
	s := utcTime(t.UnixMilli())
	return &s
}

func TestListEventsOpenAndUpcoming(t *testing.T) {
	now := testNow
	out, text := listEvents(t, &fakeData{catalog: eventsCatalog(now)}, `{}`)

	if out.Server != "CN" || !out.OpenOnly || out.Truncated || out.Note != "" || out.Source.URL != site+"/en/result/cn/stage" {
		t.Errorf("result: %+v", out)
	}
	// the time of the clock of the tools
	if out.Now != "2026-10-22T16:53:00Z" {
		t.Errorf("now %q", out.Now)
	}
	// latest first: the upcoming one opens after the open one
	want := []eventEntry{
		{ZoneID: "act53side_zone1", Name: "Until the Earth Turns Lime", Status: "upcoming", OpenTime: ms(now.Add(2 * day)), CloseTime: ms(now.Add(16 * day)),
			StageCount: 1, Stages: []eventStage{{"act53side_01", "LE-1"}}},
		// not its gacha
		{ZoneID: "act52side_zone1", Name: "昨日海", Status: "open", OpenTime: ms(now.Add(-13 * day)), CloseTime: ms(now.Add(3 * time.Hour)),
			StageCount: 3, Stages: []eventStage{{"act52side_01", "YW-1"}, {"act52side_02", "YW-2"}, {"act52side_04", "YW-4"}}},
	}
	got, _ := json.Marshal(out.Events)
	if w, _ := json.Marshal(want); string(got) != string(w) {
		t.Errorf("events:\n%s\nwant:\n%s", got, w)
	}

	// the text alone is enough to answer: the events, their times and stages, and the credit
	for _, s := range []string{
		"Events open or upcoming on CN at " + out.Now,
		"| event | zoneId | status | opens | closes | rerun | stages |",
		"| 昨日海 | act52side_zone1 | open | " + *ms(now.Add(-13 * day)) + " | " + *ms(now.Add(3 * time.Hour)) + " | no | 3 |",
		"| Until the Earth Turns Lime | act53side_zone1 | upcoming | ",
		"\n\nStages of act53side_zone1: LE-1 (act53side_01)\nStages of act52side_zone1: YW-1 (act52side_01), YW-2 (act52side_02), YW-4 (act52side_04)\n",
		// the credit apart from the stages
		"YW-4 (act52side_04)\n\nData: Penguin Statistics",
		"Data: Penguin Statistics (https://penguin-stats.io), CC BY-NC 4.0 (https://creativecommons.org/licenses/by-nc/4.0/). Source: https://penguin-stats.io/en/result/cn/stage",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("text lacks %q:\n%s", s, text)
		}
	}
	if strings.Contains(text, `"events"`) || strings.Contains(text, "炼金无限池") {
		t.Errorf("text:\n%s", text)
	}
}

// With no event open or upcoming, the latest ones still tell when the last one was.
func TestListEventsNoneOpen(t *testing.T) {
	now := testNow
	out, text := listEvents(t, &fakeData{catalog: eventsCatalog(now)}, `{"server":"US"}`)

	if ids := zoneIDs(out.Events); strings.Join(ids, ",") != "act11sre_zone1,act13d2_zone1,act45side_zone1" {
		t.Fatalf("events %v", ids)
	}
	rerun := map[string]bool{"act11sre_zone1": true, "act13d2_zone1": true, "act45side_zone1": false}
	for _, e := range out.Events {
		if e.Status != "closed" || e.IsRerun != rerun[e.ZoneID] || e.StageCount != 1 || len(e.Stages) != 1 {
			t.Errorf("event %+v", e)
		}
	}
	// the stage of the rerun on CN only is not one on US
	if s := out.Events[0].Stages[0]; s.StageID != "act18d3_01_rep" {
		t.Errorf("stages of %s: %+v", out.Events[0].ZoneID, out.Events[0].Stages)
	}
	if out.Server != "US" || !out.OpenOnly || out.Truncated || !strings.HasPrefix(out.Note, "No event is open or upcoming on US now: these are the latest 3, closed.") ||
		!strings.Contains(out.Note, "global servers") || !strings.Contains(out.Note, "opened on 2026-09-22") || out.Source.URL != site+"/en/result/us/stage" {
		t.Errorf("result: %+v", out)
	}
	for _, s := range []string{"Events on US at ", out.Note, "| Under Tides - Rerun | act11sre_zone1 | closed | " + *ms(now.Add(-30 * day)) + " | " + *ms(now.Add(-20 * day)) + " | yes | 1 |"} {
		if !strings.Contains(text, s) {
			t.Errorf("text lacks %q:\n%s", s, text)
		}
	}

	// CN with none open: no word on the global servers
	out, _ = listEvents(t, &fakeData{catalog: eventsCatalog(now.Add(-35 * day))}, `{}`)
	if len(out.Events) != 3 || out.Events[0].ZoneID != "act53side_zone1" || strings.Contains(out.Note, "global") {
		t.Errorf("CN: %+v", out)
	}
}

func TestListEventsAll(t *testing.T) {
	now := testNow
	data := &fakeData{catalog: eventsCatalog(now)}

	out, text := listEvents(t, data, `{"open_only":false}`)
	// events only, latest first
	if ids := zoneIDs(out.Events); strings.Join(ids, ",") != "act53side_zone1,act52side_zone1,act11sre_zone1,act13d2_zone1,act45side_zone1,act10d5_zone1" {
		t.Errorf("events %v", ids)
	}
	if out.OpenOnly || out.Truncated || out.Note != "" {
		t.Errorf("result: %+v", out)
	}
	for _, e := range out.Events {
		if e.Stages != nil {
			t.Errorf("stages of %s: %+v", e.ZoneID, e.Stages)
		}
	}
	if !strings.Contains(text, "Events on CN at "+out.Now+", latest first") || strings.Contains(text, "Stages of") {
		t.Errorf("text:\n%s", text)
	}

	_, app := testServer(data)
	res := callTool(t, app, "list_events", `{"open_only":false,"limit":3,"lang":"ja"}`)
	if err := json.Unmarshal(res.StructuredContent, &out); err != nil {
		t.Fatal(err)
	}
	if ids := zoneIDs(out.Events); strings.Join(ids, ",") != "act53side_zone1,act52side_zone1,act11sre_zone1" || !out.Truncated ||
		out.Events[2].Name != "潮汐の下・復刻" || out.Events[2].StageCount != 2 || out.Source.URL != site+"/ja/result/cn/stage" {
		t.Errorf("result: %+v", out)
	}
	if strings.Contains(string(res.StructuredContent), `"stages"`) {
		t.Errorf("stages: %s", res.StructuredContent)
	}
	if text := res.text(t); !strings.Contains(text, "\n\nShowing the latest 3 events: raise limit (at most 50) for older ones.\n") {
		t.Errorf("text:\n%s", text)
	}
}

func TestListEventsArguments(t *testing.T) {
	_, app := testServer(&fakeData{catalog: eventsCatalog(testNow)})
	for _, args := range []string{`{"limit":0}`, `{"limit":51}`, `{"server":"TW"}`, `{"open_only":"yes"}`} {
		if res := callTool(t, app, "list_events", args); !res.IsError {
			t.Errorf("%s: %s", args, res.StructuredContent)
		}
	}

	// no arguments at all: the defaults (see observe)
	if res := callTool(t, app, "list_events", "null"); res.IsError {
		t.Errorf("null: %s", res.text(t))
	}

	// open_only is a boolean, true unless set
	_, b := post(t, app, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "Mcp-Protocol-Version", legacyVersion)
	var res struct {
		Tools []struct {
			Name        string
			InputSchema struct {
				Properties map[string]map[string]any `json:"properties"`
				Required   []string                  `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(decode(t, b).Result, &res); err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "list_events" {
			continue
		}
		p := tool.InputSchema.Properties
		if p["open_only"]["type"] != "boolean" || p["open_only"]["default"] != true || p["limit"]["default"] != 20.0 ||
			p["limit"]["maximum"] != 50.0 || p["server"]["default"] != "CN" || len(tool.InputSchema.Required) != 0 {
			t.Errorf("input schema: %v", p)
		}
		return
	}
	t.Fatal("no list_events")
}

func TestListEventsWhileCold(t *testing.T) {
	_, app := testServer(&fakeData{})
	res := callTool(t, app, "list_events", `{}`)
	if !res.IsError || res.text(t) != errCold.Error() {
		t.Errorf("result: %+v", res)
	}
}

func TestEventStatus(t *testing.T) {
	now := time.Date(2026, 10, 22, 20, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).UnixMilli(), 10) }
	for _, tc := range []struct {
		existence, want string
	}{
		{`{"exist":true,"openTime":` + at(0) + `,"closeTime":` + at(time.Hour) + `}`, "open"},
		{`{"exist":true,"openTime":` + at(-time.Hour) + `,"closeTime":` + at(0) + `}`, "closed"},
		{`{"exist":true,"openTime":` + at(time.Millisecond) + `,"closeTime":` + at(time.Hour) + `}`, "upcoming"},
		{`{"exist":true,"openTime":` + at(-time.Hour) + `,"closeTime":62141368179000}`, "open"},
		{`{"exist":true}`, "open"},
	} {
		var e service.SEOExistence
		if err := json.Unmarshal([]byte(`{"CN":`+tc.existence+`}`), &e); err != nil {
			t.Fatal(err)
		}
		if got := eventStatus(e, "CN", now); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.existence, got, tc.want)
		}
	}
	if eventTime(null.IntFrom(constant.FakeEndTimeMilli)) != nil || eventTime(null.IntFrom(0)) != nil || eventTime(null.Int{}) != nil {
		t.Error("a time of none")
	}
	if got := eventTime(null.IntFrom(1792699200000)); got == nil || *got != "2026-10-22T20:00:00Z" {
		t.Errorf("eventTime = %v", got)
	}
}

// Within the budget of a result (about 25k tokens) even with 50 events open at once, each with the most stages and
// the longest name an event has (October 2026): 64 KB of this text and JSON, mostly ASCII, is about 16k tokens.
func TestListEventsSize(t *testing.T) {
	now := testNow
	var zones []*model.Zone
	var stages []*model.Stage
	for z := range 150 {
		id := "act" + strconv.Itoa(z) + "side_zone1"
		zones = append(zones, &model.Zone{ArkZoneID: id, ZoneID: z, Category: "ACTIVITY",
			Name:      i18n("理想城：长夏狂欢季・复刻", "Come Catastrophes or Wakes of Vultures - Rerun", "理想都市：エンドレスカーニバル・復刻", "이상도시: 엔드리스 카니발 복각"),
			Existence: eventExistence(map[string]eventWindow{"CN": {now.Add(-time.Hour - time.Duration(z)*time.Minute), now.Add(time.Hour)}})})
		for s := range 13 {
			code := "IC-" + strconv.Itoa(s)
			stages = append(stages, &model.Stage{ArkStageID: "act" + strconv.Itoa(z) + "side_" + strconv.Itoa(s) + "_rep", StageID: z*100 + s, ZoneID: z,
				Code: i18n(code, code, code, code), Existence: existence("CN")})
		}
	}
	_, app := testServer(&fakeData{catalog: service.NewSEOCatalog(nil, stages, zones)})
	for _, args := range []string{`{"limit":50}`, `{"limit":50,"open_only":false}`} {
		res := callTool(t, app, "list_events", args)
		var out eventList
		if err := json.Unmarshal(res.StructuredContent, &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Events) != 50 || !out.Truncated {
			t.Fatalf("%s: %d events", args, len(out.Events))
		}
		if n := len(res.StructuredContent) + len(res.text(t)); n > 64<<10 {
			t.Errorf("%s: result of %d bytes", args, n)
		}
	}
}
