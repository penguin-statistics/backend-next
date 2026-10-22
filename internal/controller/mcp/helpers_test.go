package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/service"
)

const site = "https://penguin-stats.io"

var lastModified = time.Date(2026, 10, 22, 16, 32, 1, 0, time.UTC)

type fakeData struct {
	catalog  *service.SEOCatalog
	matrices map[string]*service.SEOMatrix
}

func (d *fakeData) Catalog() *service.SEOCatalog            { return d.catalog }
func (d *fakeData) Matrix(server string) *service.SEOMatrix { return d.matrices[server] }

func i18n(zh, en, ja, ko string) []byte {
	b, _ := json.Marshal(map[string]string{"zh": zh, "en": en, "ja": ja, "ko": ko})
	return b
}

// existence is the existence of what opened on 2019-05-01 and never closes, on the servers.
func existence(servers ...string) []byte {
	return between(1556676000000, 0, servers...)
}

// between is the existence of what opens at open and closes at end (epoch milliseconds, 0 for none), on the servers.
func between(open, end int64, servers ...string) []byte {
	m := map[string]map[string]any{}
	for _, s := range []string{"CN", "US", "JP", "KR"} {
		m[s] = map[string]any{"exist": false}
	}
	for _, s := range servers {
		e := map[string]any{"exist": true, "openTime": open}
		if end != 0 {
			e["closeTime"] = end
		}
		m[s] = e
	}
	b, _ := json.Marshal(m)
	return b
}

// keywords are the aliases and readings of an item in zh and ja.
func keywords(zhAlias, jaAlias, zhPron, jaPron []string) []byte {
	b, _ := json.Marshal(map[string]map[string][]string{
		"alias": {"zh": zhAlias, "ja": jaAlias},
		"pron":  {"zh": zhPron, "ja": jaPron},
	})
	return b
}

var all = []string{"CN", "US", "JP", "KR"}

// testNow is when the data of the fixtures was taken from the API: the event act52side_zone1 is open on CN until
// 20:00Z.
var testNow = time.Date(2026, 10, 22, 16, 53, 0, 0, time.UTC)

// testCatalog is a part of the catalog of 2026-10-22, with the names, codes, aliases, sanity costs and CN times the API
// had (the numerical IDs are made up, and the other servers have the times of CN): the four tiers of Orirock, which
// share aliases, the two items named Carbon Brick in en, items of types not listed (LMD, and D32 Steel, crafted in the
// workshop); stages whose codes collide (main_ and tough_, an event with its permanent stage and rerun, and another
// event), the open event, a gacha and the recruitment; the best stages for Orirock Cube (see testMatrices), and a past
// event.
func testCatalog() *service.SEOCatalog {
	return service.NewSEOCatalog(
		[]*model.Item{
			{ArkItemID: "30011", Type: "MATERIAL", SortID: 100015, Rarity: 0, Name: i18n("源岩", "Orirock", "源岩鉱", "원암"), Existence: existence(all...),
				Keywords: keywords([]string{"源岩", "土块", "石头"}, []string{"源岩鉱", "げんがんこう", "ゲンガンコウ"},
					[]string{"yuan`yan", "tu`kuai", "shi`tou"}, []string{"gengan`kou", "gengan`kou"})},
			{ArkItemID: "30012", Type: "MATERIAL", SortID: 100014, Rarity: 1, Name: i18n("固源岩", "Orirock Cube", "初級源岩", "원암 큐브"), Existence: existence(all...),
				Keywords: keywords([]string{"固源岩", "土块", "石头"}, []string{"初級源岩", "しょきゅうげんがん", "ショキュウゲンガン"},
					[]string{"gu`yuan`yan", "tu`kuai", "shi`tou"}, []string{"shokyuu`gengan", "syokyuu`gengan"})},
			{ArkItemID: "30013", Type: "MATERIAL", SortID: 100013, Rarity: 2, Name: i18n("固源岩组", "Orirock Cluster", "中級源岩", "원암 큐브 번들"), Existence: existence(all...),
				Keywords: keywords([]string{"固源岩组", "土块", "石头"}, []string{"中級源岩", "ちゅうきゅうげんがん", "チュウキュウゲンガン"},
					[]string{"gu`yuan`yan`zu", "tu`kuai", "shi`tou"}, []string{"chuukyuu`gengan", "tyuukyuu`gengan"})},
			{ArkItemID: "30014", Type: "MATERIAL", SortID: 100012, Rarity: 3, Name: i18n("提纯源岩", "Orirock Concentration", "上級源岩", "정제 원암"), Existence: existence(all...),
				Keywords: keywords([]string{"提纯源岩", "土块", "石头"}, []string{"上級源岩", "じょうきゅうげんがん", "ジョウキュウゲンガン"},
					[]string{"ti`chun`yuan`yan", "tu`kuai", "shi`tou"}, []string{"joukyuu`gengan", "joukyuu`gengan"})},
			{ArkItemID: "3113", Type: "MATERIAL", SortID: 200007, Rarity: 2, Name: i18n("碳素", "Carbon Brick", "中級炭素材", "카본 번들"), Existence: existence(all...),
				Keywords: keywords([]string{"碳素"}, []string{"中級炭素材"}, []string{"tan`su"}, []string{"chuukyuu`tan`sozai"})},
			// the en name of 3114 is that of 3113 in the data (Carbon Pack in the game)
			{ArkItemID: "3114", Type: "MATERIAL", SortID: 200006, Rarity: 3, Name: i18n("碳素组", "Carbon Brick", "上級炭素材", "카본 팩"), Existence: existence(all...),
				Keywords: keywords([]string{"碳素组"}, []string{"上級炭素材"}, []string{"tan`su`zu"}, []string{"joukyuu`tan`sozai"})},
			{ArkItemID: "4001_1000", Type: "TEMP", SortID: -10000, Rarity: 3, Name: i18n("龙门币1000", "LMD1000", "龍門幣1000", "용문폐1000"), Existence: existence(all...),
				Keywords: keywords([]string{"龙门币1000", "钱"}, nil, []string{"long`men`bi`1000", "qian"}, nil)},
			{ArkItemID: "30135", Type: "ARKPLANNER", SortID: 100030, Rarity: 4, Name: i18n("D32钢", "D32 Steel", "D32鋼", "D32 강철"), Existence: existence(all...),
				Keywords: keywords([]string{"D32钢"}, nil, []string{"D32`gang"}, nil)},
			// in no matrix
			{ArkItemID: "4005", Type: "LGG_SHD", SortID: 10007, Rarity: 2, Name: i18n("资质凭证", "Commendation Certificate", "一般資格証", "자격증명서"), Existence: existence(all...),
				Keywords: keywords([]string{"资质凭证", "绿票"}, []string{"一般資格証"}, []string{"zi`zhi`ping`zheng", "lv`piao"}, []string{"ippan`shikaku`shou"})},
			{ArkItemID: "recruit_tag_senior_operator", Type: "RECRUIT_TAG", SortID: -10000, Name: i18n("资深干员", "Senior Operator", "エリート", "특별채용"), Existence: existence(all...)},
		},
		[]*model.Stage{
			{ArkStageID: "main_01-07", StageID: 7, ZoneID: 1, StageType: "MAIN", Code: i18n("1-7", "1-7", "1-7", "1-7"), Sanity: null.IntFrom(6), Existence: existence(all...)},
			{ArkStageID: "recruit", StageID: 8, ZoneID: 99, StageType: "MAIN", Code: i18n("公开招募", "Recruit", "公開求人", "공개모집"), Sanity: null.IntFrom(99), Existence: existence(all...)},
			{ArkStageID: "randomMaterial_1", StageID: 100, ZoneID: 98, StageType: "ACTIVITY", Code: i18n("罗德岛物资补给", "Rhodes Island Supplies", "補給物資・ロドス", "로도스 아일랜드 보급 물자"),
				Sanity: null.IntFrom(99), ExtraProcessType: null.StringFrom("GACHABOX"), Existence: between(1577174400000, 0, all...)},
			{ArkStageID: "act10d5_01", StageID: 200, ZoneID: 20, StageType: "ACTIVITY", Code: i18n("SV-1", "SV-1", "SV-1", "SV-1"), Sanity: null.IntFrom(6), Existence: between(1592467200000, 1593028800000, all...)},
			{ArkStageID: "act18d3_01", StageID: 300, ZoneID: 30, StageType: "ACTIVITY", Code: i18n("SV-1", "SV-1", "SV-1", "SV-1"), Sanity: null.IntFrom(12), Existence: between(1619856000000, 1621022400000, all...)},
			{ArkStageID: "act18d3_01_perm", StageID: 310, ZoneID: 31, StageType: "ACTIVITY", Code: i18n("SV-1", "SV-1", "SV-1", "SV-1"), Sanity: null.IntFrom(12), Existence: between(1621627200000, 0, all...)},
			{ArkStageID: "act18d3_01_rep", StageID: 320, ZoneID: 32, StageType: "ACTIVITY", Code: i18n("SV-1", "SV-1", "SV-1", "SV-1"), Sanity: null.IntFrom(12), Existence: between(1653897600000, 1654718400000, all...)},
			{ArkStageID: "main_10-01", StageID: 400, ZoneID: 10, StageType: "MAIN", Code: i18n("10-2", "10-2", "10-2", "10-2"), Sanity: null.IntFrom(21), Existence: between(1649923200000, 0, all...)},
			{ArkStageID: "tough_10-01", StageID: 401, ZoneID: 11, StageType: "MAIN", Code: i18n("10-2", "10-2", "10-2", "10-2"), Sanity: null.IntFrom(21), Existence: between(1649923200000, 0, all...)},
			{ArkStageID: "main_11-06", StageID: 450, ZoneID: 12, StageType: "MAIN", Code: i18n("11-7", "11-7", "11-7", "11-7"), Sanity: null.IntFrom(21), Existence: between(1665475200000, 0, all...)},
			{ArkStageID: "tough_11-06", StageID: 451, ZoneID: 13, StageType: "MAIN", Code: i18n("11-7", "11-7", "11-7", "11-7"), Sanity: null.IntFrom(21), Existence: between(1665475200000, 0, all...)},
			{ArkStageID: "act52side_04", StageID: 900, ZoneID: 52, StageType: "ACTIVITY", Code: i18n("YW-4", "YW-4", "YW-4", "YW-4"), Sanity: null.IntFrom(12), Existence: between(1791518400000, 1792699200000, "CN")},
			{ArkStageID: "sub_02-12", StageID: 500, ZoneID: 2, StageType: "SUB", Code: i18n("S2-12", "S2-12", "S2-12", "S2-12"), Sanity: null.IntFrom(15), Existence: existence(all...)},
			{ArkStageID: "main_14-02", StageID: 510, ZoneID: 14, StageType: "MAIN", Code: i18n("14-3", "14-3", "14-3", "14-3"), Sanity: null.IntFrom(21), Existence: between(1714550400000, 0, all...)},
			{ArkStageID: "act9d0_05_perm", StageID: 600, ZoneID: 40, StageType: "ACTIVITY", Code: i18n("DM-5", "DM-5", "DM-5", "DM-5"), Sanity: null.IntFrom(15), Existence: between(1618473600000, 0, all...)},
			{ArkStageID: "act11d0_01_perm", StageID: 610, ZoneID: 41, StageType: "ACTIVITY", Code: i18n("TW-1", "TW-1", "TW-1", "TW-1"), Sanity: null.IntFrom(9), Existence: between(1625083200000, 0, all...)},
			{ArkStageID: "act19side_05_perm", StageID: 620, ZoneID: 42, StageType: "ACTIVITY", Code: i18n("DV-5", "DV-5", "DV-5", "DV-5"), Sanity: null.IntFrom(15), Existence: between(1690833600000, 0, "CN")},
			{ArkStageID: "act21mini_05", StageID: 800, ZoneID: 50, StageType: "ACTIVITY", Code: i18n("BD-5", "BD-5", "BD-5", "BD-5"), Sanity: null.IntFrom(15), Existence: between(1783656000000, 1784232000000, "CN")},
		},
		[]*model.Zone{
			{ArkZoneID: "main_1", ZoneID: 1, Index: 1, Category: "MAINLINE", Name: i18n("第一章", "Episode 1", "第一章", "에피소드 1"), Existence: existence(all...)},
			{ArkZoneID: "main_10", ZoneID: 10, Index: 10, Category: "MAINLINE", Name: i18n("第十章 (标准)", "Episode 10 (Standard)", "第十章 (通常)", "에피소드 10 (Standard)"), Existence: between(1649923200000, 0, all...)},
			{ArkZoneID: "main_10_tough", ZoneID: 11, Index: 11, Category: "MAINLINE", Name: i18n("第十章 (磨难)", "Episode 10 (Tough)", "第十章 (厄難)", "에피소드 10 (Tough)"), Existence: between(1649923200000, 0, all...)},
			{ArkZoneID: "main_11", ZoneID: 12, Index: 12, Category: "MAINLINE", Name: i18n("第十一章 (标准)", "Episode 11 (Standard)", "第十一章 (通常)", "에피소드 11 (Standard)"), Existence: between(1665475200000, 0, all...)},
			{ArkZoneID: "main_11_tough", ZoneID: 13, Index: 13, Category: "MAINLINE", Name: i18n("第十一章 (磨难)", "Episode 11 (Tough)", "第十一章 (厄難)", "에피소드 11 (Tough)"), Existence: between(1665475200000, 0, all...)},
			{ArkZoneID: "act10d5_zone1", ZoneID: 20, Index: 1008, Category: "ACTIVITY", Name: i18n("乌萨斯的孩子们", "Children of Ursus", "ウルサスの子供たち", "우르수스의 아이들"), Existence: between(1592467200000, 1593028800000, all...)},
			{ArkZoneID: "act18d3_zone1", ZoneID: 30, Index: 1021, Category: "ACTIVITY", Name: i18n("覆潮之下", "Under Tides", "潮汐の下", "언더 타이즈"), Existence: between(1619856000000, 1621022400000, all...)},
			{ArkZoneID: "permanent_sub_3_zone1", ZoneID: 31, Index: 2003, Category: "ACTIVITY_PERMANENT", Type: null.StringFrom("INTERLUDE"),
				Name: i18n("覆潮之下・插曲", "Under Tides - Intermezzi", "潮汐の下・エピソード", "언더 타이즈 에피소드"), Existence: between(1621627200000, 0, all...)},
			{ArkZoneID: "act11sre_zone1", ZoneID: 32, Index: 1038, Category: "ACTIVITY", Name: i18n("覆潮之下・复刻", "Under Tides - Rerun", "潮汐の下・復刻", "언더 타이즈 재개방"), Existence: between(1653897600000, 1654718400000, all...)},
			{ArkZoneID: "act52side_zone1", ZoneID: 52, Index: 1118, Category: "ACTIVITY", Name: i18n("昨日海", "昨日海", "昨日海", "昨日海"), Existence: between(1791518400000, 1792699200000, "CN")},
			{ArkZoneID: "gachabox", ZoneID: 98, Category: "GACHABOX", Name: i18n("物资补给", "Supplies", "補給物資", "보급물자"), Existence: existence(all...)},
			{ArkZoneID: "recruit", ZoneID: 99, Category: "RECRUIT", Name: i18n("公开招募", "Recruit", "公開求人", "공개모집"), Existence: existence(all...)},
			{ArkZoneID: "main_2", ZoneID: 2, Index: 2, Category: "MAINLINE", Name: i18n("第二章", "Episode 2", "第二章", "에피소드 2"), Existence: existence(all...)},
			{ArkZoneID: "main_14", ZoneID: 14, Index: 18, Category: "MAINLINE", Name: i18n("第十四章 (标准)", "Episode 14 (Standard)", "第十四章 (通常)", "에피소드 14 (Standard)"),
				Existence: between(1714550400000, 0, all...)},
			{ArkZoneID: "permanent_sub_1_zone1", ZoneID: 40, Index: 2001, Category: "ACTIVITY_PERMANENT", Type: null.StringFrom("INTERLUDE"),
				Name: i18n("生于黑夜・插曲", "Darknights Memoir - Intermezzi", "闇夜に生きる・エピソード", "흑야의 회고록 에피소드"), Existence: between(1618473600000, 0, all...)},
			{ArkZoneID: "permanent_sidestory_4_zone1", ZoneID: 41, Index: 3003, Category: "ACTIVITY_PERMANENT", Type: null.StringFrom("SIDESTORY"),
				Name: i18n("沃伦姆德的薄暮・别传", "Twilight of Wolumonde - Side Story", "ウォルモンドの薄暮・サイドストーリー", "월루몽드의 황혼 사이드"), Existence: between(1625083200000, 0, all...)},
			{ArkZoneID: "permanent_sidestory_15_zone1", ZoneID: 42, Index: 3014, Category: "ACTIVITY_PERMANENT", Type: null.StringFrom("SIDESTORY"),
				Name: i18n("绿野幻梦・别传", "Dorothy's Vision - Side Story", "翠玉の夢・サイドストーリー", "도로시의 비전 사이드"), Existence: between(1690833600000, 0, "CN")},
			{ArkZoneID: "act21mini_zone1", ZoneID: 50, Index: 1113, Category: "ACTIVITY", Name: i18n("丛林症结", "丛林症结", "丛林症结", "丛林症结"),
				Existence: between(1783656000000, 1784232000000, "CN")},
		},
	)
}

// weeklyCatalog has a supply stage, CA-5 of 空中威胁 (WEEKLY), open every day by its existence, and an item it drops
func weeklyCatalog() *service.SEOCatalog {
	return service.NewSEOCatalog(
		[]*model.Item{{ArkItemID: "3303", Type: "MATERIAL", SortID: 1, Rarity: 2, Name: i18n("技巧概要·卷3", "Skill Summary - 3", "アーツ学III", "스킬개론 제3권"), Existence: existence(all...)}},
		[]*model.Stage{{ArkStageID: "wk_fly_5", StageID: 1, ZoneID: 1, Code: i18n("CA-5", "CA-5", "CA-5", "CA-5"), Sanity: null.IntFrom(30), Existence: existence(all...)}},
		[]*model.Zone{{ArkZoneID: "weekly_6", ZoneID: 1, Category: "WEEKLY", Name: i18n("空中威胁", "Aerial Threat", "航空脅威", "공중 위협"), Existence: existence(all...)}})
}

// testMatrices are the matrices of the servers calculated: a part of the matrix of CN of 2026-10-22, with the rows the
// API had. Orirock Cube, whose best stages by sanity per item were 1-7 (4.819), S2-12, DV-5, DM-5, TW-1 and the event
// open until 20:00Z (YW-4, 7.198), from a few stages of each kind: the event BD-5 of July 2026 (6.533, closed), a gacha,
// and a row of few runs (made up) better than 1-7. Rows that ended on stages still open: a bonus drop of an event on
// 1-7 (of an item the catalog does not have), and Orirock on 14-3, before its drop table changed in May 2024.
func testMatrices() map[string]*service.SEOMatrix {
	return map[string]*service.SEOMatrix{
		"CN": service.NewSEOMatrix("CN", []*modelv2.OneDropMatrixElement{
			{StageID: "main_01-07", ItemID: "30012", Times: 2168783984, Quantity: 2700151376, StartTime: 1556676000000},
			{StageID: "sub_02-12", ItemID: "30012", Times: 1028653, Quantity: 2356522, StartTime: 1556676000000},
			{StageID: "act19side_05_perm", ItemID: "30012", Times: 5778, Quantity: 13159, StartTime: 1690833600000},
			{StageID: "act9d0_05_perm", ItemID: "30012", Times: 71793, Quantity: 163064, StartTime: 1618430400000},
			{StageID: "act11d0_01_perm", ItemID: "30012", Times: 816, Quantity: 1023, StartTime: 1625083200000},
			{StageID: "act52side_04", ItemID: "30012", Times: 6154517, Quantity: 10259729, StartTime: 1791518400000},
			{StageID: "act21mini_05", ItemID: "30012", Times: 188240, Quantity: 432192, StartTime: 1783656000000, EndTime: null.IntFrom(1784232000000)},
			{StageID: "main_10-01", ItemID: "30012", Times: 4001, Quantity: 1454, StartTime: 1649923200000},
			{StageID: "tough_10-01", ItemID: "30012", Times: 1844, Quantity: 661, StartTime: 1649923200000},
			{StageID: "randomMaterial_1", ItemID: "30012", Times: 19918, Quantity: 1403, StartTime: 1577174400000},
			{StageID: "act18d3_01_perm", ItemID: "30012", Times: 40, Quantity: 100, StartTime: 1621627200000},
			{StageID: "main_01-07", ItemID: "30011", Times: 2168783984, Quantity: 257543229, StartTime: 1556676000000},
			{StageID: "main_01-07", ItemID: "randomMaterial_1", Times: 29655, Quantity: 3619, StartTime: 1577174400000, EndTime: null.IntFrom(1578340800000)},
			{StageID: "sub_02-12", ItemID: "30011", Times: 1028653, Quantity: 221852, StartTime: 1556676000000},
			{StageID: "main_14-02", ItemID: "30011", Times: 32992, Quantity: 48079, StartTime: 1714550400000, EndTime: null.IntFrom(1715716800000)},
			{StageID: "recruit", ItemID: "recruit_tag_senior_operator", Times: 1224363319, Quantity: 1977025, StartTime: 1660593600000},
		}, lastModified),
	}
}

// testServer returns a server of data, and its registry for tests to add tools to.
func testServer(data Data) (*registry, *fiber.App) {
	srv, r := newServer(&tools{data: data, site: site, clock: func() time.Time { return testNow }}, "test")
	app := fiber.New()
	Mount(app, srv, 1000, time.Minute)
	return r, app
}

// probeIn is the input of the tool probe of the tests: n, 1 by default
type probeIn struct {
	N int `json:"n,omitempty"`
}

// addProbe adds the tool probe, which answers its input, to r, and returns the number of its calls.
func addProbe(r *registry) *atomic.Int64 {
	in, err := jsonschema.For[probeIn](nil)
	if err != nil {
		panic(err)
	}
	in.Properties["n"].Default = json.RawMessage("1")
	var calls atomic.Int64
	add(r, &sdkmcp.Tool{Name: "probe", Title: "Probe", Description: "Answers its input.", InputSchema: in},
		func(_ context.Context, _ *sdkmcp.CallToolRequest, in probeIn) (*sdkmcp.CallToolResult, probeIn, error) {
			calls.Add(1)
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: strconv.Itoa(in.N)}}}, in, nil
		})
	return &calls
}

const (
	legacyVersion = "2025-11-25"
	newVersion    = "2026-07-28"
)

// post posts a JSON-RPC message to /mcp as an MCP client does, with the headers given
func post(t *testing.T, app *fiber.App, body string, header ...string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(fiber.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(fiber.HeaderAccept, "application/json, text/event-stream")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decode(t *testing.T, b []byte) rpcResponse {
	t.Helper()
	var r rpcResponse
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("not a JSON-RPC response: %v: %s", err, b)
	}
	return r
}

// toolResult is a CallToolResult as sent
type toolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

// callTool calls a tool with the 2025-11-25 protocol, which needs no handshake when stateless, and returns its result
func callTool(t *testing.T, app *fiber.App, name, args string) toolResult {
	t.Helper()
	resp, b := post(t, app, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`,
		"Mcp-Protocol-Version", legacyVersion)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	r := decode(t, b)
	if r.Error != nil {
		t.Fatalf("JSON-RPC error %d %s", r.Error.Code, r.Error.Message)
	}
	var res toolResult
	if err := json.Unmarshal(r.Result, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

// text is the text content of a result, which every tool gives as one block
func (r toolResult) text(t *testing.T) string {
	t.Helper()
	if len(r.Content) != 1 || r.Content[0].Type != "text" {
		t.Fatalf("content: %+v", r.Content)
	}
	return r.Content[0].Text
}

func histogramCount(t *testing.T, o prometheus.Observer) uint64 {
	t.Helper()
	var m dto.Metric
	if err := o.(prometheus.Metric).Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram().GetSampleCount()
}
