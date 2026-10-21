package seo

import "strings"

// texts are the texts of the pages in a language. Labels shared with the frontend are copied from its locales
// (frontend-v2 src/locales/*.json, v3.13.32) so that the page reads the same before and after the app starts; the
// key is noted next to each.
type texts struct {
	// app.name
	SiteName string
	// <html lang> and hreflang
	Lang     string
	OGLocale string
	// the servers as named in titles and text
	Servers map[string]string
	// server.name-ish label of the server navigation
	ServerLabel     string
	BreadcrumbLabel string

	// menu.stats.item, menu.stats.stage
	ByItem  string
	ByStage string
	// {list} ({server}): the name of a list page in breadcrumbs
	ListCrumb string

	// titles, without the site name
	ItemTitle      string
	StageTitle     string
	ItemListTitle  string
	StageListTitle string
	NotFoundTitle  string

	// result.title, stats.title: the headings of the frontend
	ItemHeading  string
	StageHeading string

	// stats.headers
	HStage, HItem, HQuantity, HTimes, HPercentage, HAPPPR, HAPCost, HTimeRange string
	// stats.timeRange.inBetween, stats.timeRange.toPresent
	InBetween string
	ToPresent string

	// item.categories, zone.types
	ItemCategories map[string]string
	ZoneCategories map[string]string
	// item.related
	Related string

	ItemCaption  string
	StageCaption string

	// definitions of the columns
	Definitions string
	// stage pages: the sanity cost of the stage
	SanityFact string
	Updated    string
	// Stop ends a sentence
	Stop     string
	FullData string
	License  string
	Empty    string
	Cold     string
	NotFound string

	// meta descriptions
	ItemDesc            string
	StageDesc           string
	ItemListDesc        string
	StageListDesc       string
	ItemDescNoData      string
	StageDescNoData     string
	UpdatedDesc         string
	SanityDesc          string
	Tagline             string
	ItemHighlightFirst  string
	ItemHighlight       string
	StageHighlightFirst string
	StageHighlight      string
	HighlightSep        string

	// structured data
	DatasetName          string
	VarTimes             string
	VarQuantity          string
	VarRate              string
	MeasurementTechnique string
}

// langs are the languages of the stats routes of the frontend (src/router.js statsLanguages)
var langs = []string{"zh", "en", "ja", "ko"}

// defaultLang is the x-default of the hreflang links, the fallback locale of the frontend
const defaultLang = "en"

var textsByLang = map[string]*texts{
	"zh": {
		SiteName:        "企鹅物流数据统计",
		Lang:            "zh",
		OGLocale:        "zh_CN",
		Servers:         map[string]string{"CN": "国服", "US": "美服", "JP": "日服", "KR": "韩服"},
		ServerLabel:     "服务器",
		BreadcrumbLabel: "导航路径",
		ByItem:          "按素材",
		ByStage:         "按作战",
		ListCrumb:       "{list}（{server}）",
		ItemTitle:       "{name} 掉率统计（{server}）",
		StageTitle:      "{stage} 掉落统计（{server}）",
		ItemListTitle:   "素材掉率 · 按素材（{server}）",
		StageListTitle:  "素材掉率 · 按作战（{server}）",
		NotFoundTitle:   "未找到该页面",
		ItemHeading:     "{item} 统计结果",
		StageHeading:    "{stage} 统计结果",
		HStage:          "作战", HItem: "物品", HQuantity: "掉落数", HTimes: "样本数", HPercentage: "百分比",
		HAPPPR: "单件期望理智", HAPCost: "理智", HTimeRange: "统计区间",
		InBetween: "{0} ~ {1}",
		ToPresent: "{date} 至今",
		ItemCategories: map[string]string{
			"MATERIAL": "材料", "CARD_EXP": "作战记录", "CHIP": "芯片", "FURN": "家具", "ACTIVITY_ITEM": "活动道具",
		},
		ZoneCategories: map[string]string{
			"MAINLINE": "主题曲", "ACTIVITY_PERMANENT": "插曲 & 别传", "ACTIVITY": "限时活动", "WEEKLY": "资源收集", "GACHABOX": "物资补给箱",
		},
		Related:      "相关物品",
		ItemCaption:  "{name}在{server}各作战的掉落统计，按百分比从高到低排列，显示 {shown} / {total} 个作战",
		StageCaption: "{stage}（{server}）各物品的掉落统计，按百分比从高到低排列",
		Definitions: "样本数：统计区间内汇报的该作战次数。掉落数：这些作战中该物品的掉落总数。" +
			"百分比 = 掉落数 ÷ 样本数，即每次作战的期望掉落数量，可超过 100%。单件期望理智 = 理智 ÷ 百分比。" +
			"统计区间：该作战掉落列表最近一次变动以来的时间段。",
		SanityFact:           "理智：{sanity}",
		Updated:              "数据更新于",
		FullData:             "完整数据（JSON）",
		License:              "数据许可：",
		Stop:                 "。",
		Empty:                "暂无掉落数据。",
		Cold:                 "掉落数据计算中，请稍后再来。",
		NotFound:             "未找到该页面：物品或作战不存在，或不在该服务器开放。",
		ItemDesc:             "{name}（{server}）掉落统计：{highlights}。共 {n} 个作战。",
		StageDesc:            "{stage}（{server}）掉落统计：{highlights}；样本数 {n}（{window}）。",
		ItemListDesc:         "明日方舟各物品在{server}的掉率统计：共 {items} 种物品、{stages} 个作战。",
		StageListDesc:        "明日方舟{server}各作战的物品掉率统计：共 {stages} 个作战、{items} 种物品。",
		ItemDescNoData:       "{name}（{server}）在明日方舟各作战的掉率统计。",
		StageDescNoData:      "{stage}（{server}）的物品掉率统计。",
		UpdatedDesc:          "数据更新于 {date}。",
		SanityDesc:           "理智消耗 {sanity}。",
		Tagline:              "企鹅物流数据统计——玩家众包的明日方舟掉率数据。",
		ItemHighlightFirst:   "{code} 百分比 {p}（样本数 {n}）",
		ItemHighlight:        "{code} {p}（{n}）",
		StageHighlightFirst:  "{item} 百分比 {p}",
		StageHighlight:       "{item} {p}",
		HighlightSep:         "、",
		DatasetName:          "{name} 掉落统计（{server}）",
		VarTimes:             "统计区间内汇报的该作战次数",
		VarQuantity:          "这些作战中该物品的掉落总数",
		VarRate:              "掉落数 ÷ 样本数，即每次作战的期望掉落数量，可超过 100%",
		MeasurementTechnique: "玩家向企鹅物流数据统计提交的游戏内掉落汇报（手动录入与截图识别）",
	},
	"en": {
		SiteName:        "Penguin Statistics",
		Lang:            "en",
		OGLocale:        "en_US",
		Servers:         map[string]string{"CN": "CN server", "US": "US server", "JP": "JP server", "KR": "KR server"},
		ServerLabel:     "Server",
		BreadcrumbLabel: "Breadcrumb",
		ByItem:          "By item",
		ByStage:         "By stage",
		ListCrumb:       "{list} ({server})",
		ItemTitle:       "{name} Drop Rates ({server})",
		StageTitle:      "{stage} Drop Rates ({server})",
		ItemListTitle:   "Drop Rates by Item ({server})",
		StageListTitle:  "Drop Rates by Stage ({server})",
		NotFoundTitle:   "Page not found",
		ItemHeading:     "Statistics of {item}",
		StageHeading:    "Statistics of {stage}",
		HStage:          "Stage", HItem: "Item", HQuantity: "Loots", HTimes: "Samples", HPercentage: "Percentage",
		HAPPPR: "Sanity Required Per Item", HAPCost: "Sanity", HTimeRange: "Time Range",
		InBetween: "{0} to {1}",
		ToPresent: "{date} until now",
		ItemCategories: map[string]string{
			"MATERIAL": "Materials", "CARD_EXP": "Battle Records", "CHIP": "Chips", "FURN": "Furniture", "ACTIVITY_ITEM": "Event Items",
		},
		ZoneCategories: map[string]string{
			"MAINLINE": "Main Theme", "ACTIVITY_PERMANENT": "Intermezzi & Side Story", "ACTIVITY": "Event", "WEEKLY": "Supplies", "GACHABOX": "Gachabox",
		},
		Related:      "Related Items",
		ItemCaption:  "Drops of {name} by stage on the {server}, by percentage, highest first: {shown} of {total} stages",
		StageCaption: "Item drops of {stage} on the {server}, by percentage, highest first",
		Definitions: "Samples: runs of the stage reported in the time range. Loots: total number of the item dropped in those runs. " +
			"Percentage = Loots ÷ Samples, the expected number dropped per run, which can exceed 100%. " +
			"Sanity Required Per Item = Sanity ÷ Percentage. Time Range: since the drop table of the stage last changed.",
		SanityFact:           "Sanity: {sanity}",
		Updated:              "Updated",
		FullData:             "Full data (JSON)",
		License:              "License: ",
		Stop:                 ". ",
		Empty:                "No drop data yet.",
		Cold:                 "The drop data is being calculated. Please check back later.",
		NotFound:             "Page not found: the item or stage does not exist, or is not on this server.",
		ItemDesc:             "{name} drop rates on the {server}: {highlights}; {n} stages in total. ",
		StageDesc:            "{stage} drop rates on the {server}: {highlights}; {n} samples ({window}). ",
		ItemListDesc:         "Arknights drop rates by item on the {server}: {items} items across {stages} stages. ",
		StageListDesc:        "Arknights drop rates by stage on the {server}: {stages} stages dropping {items} items. ",
		ItemDescNoData:       "Drop rates of {name} in Arknights stages on the {server}. ",
		StageDescNoData:      "Item drop rates of {stage} on the {server}. ",
		UpdatedDesc:          "Updated {date}. ",
		SanityDesc:           "Sanity cost {sanity}. ",
		Tagline:              "Crowdsourced Arknights drop data by Penguin Statistics.",
		ItemHighlightFirst:   "{code} {p} ({n} samples)",
		ItemHighlight:        "{code} {p} ({n})",
		StageHighlightFirst:  "{item} {p}",
		StageHighlight:       "{item} {p}",
		HighlightSep:         ", ",
		DatasetName:          "{name} drop statistics ({server})",
		VarTimes:             "Runs of the stage reported in the time range",
		VarQuantity:          "Total number of the item dropped in those runs",
		VarRate:              "Loots ÷ Samples, the expected number dropped per run, which can exceed 100%",
		MeasurementTechnique: "Crowdsourced in-game drop reports submitted by players (manual entry and screenshot recognition)",
	},
	"ja": {
		SiteName:        "ペンギン急便データ統計処理部門",
		Lang:            "ja",
		OGLocale:        "ja_JP",
		Servers:         map[string]string{"CN": "大陸版", "US": "英語版", "JP": "日本版", "KR": "韓国版"},
		ServerLabel:     "サーバー",
		BreadcrumbLabel: "パンくずリスト",
		ByItem:          "素材別",
		ByStage:         "作戦別",
		ListCrumb:       "{list}（{server}）",
		ItemTitle:       "{name} ドロップ率（{server}）",
		StageTitle:      "{stage} ドロップ率（{server}）",
		ItemListTitle:   "素材別ドロップ率（{server}）",
		StageListTitle:  "作戦別ドロップ率（{server}）",
		NotFoundTitle:   "ページが見つかりません",
		ItemHeading:     "{item} 統計結果",
		StageHeading:    "{stage} 統計結果",
		HStage:          "ステージ", HItem: "アイテム", HQuantity: "ドロップ数", HTimes: "サンプル数", HPercentage: "ドロップ率",
		HAPPPR: "１個あたりの必要理性期待値", HAPCost: "理性", HTimeRange: "期間",
		InBetween: "{0} ~ {1}",
		ToPresent: "{date} ~ 現在",
		ItemCategories: map[string]string{
			"MATERIAL": "素材", "CARD_EXP": "作戦記録", "CHIP": "SoC", "FURN": "家具", "ACTIVITY_ITEM": "イベントアイテム",
		},
		ZoneCategories: map[string]string{
			// zone.types has no ACTIVITY in ja
			"MAINLINE": "メインテーマ", "ACTIVITY_PERMANENT": "エピソード & サイドストーリー", "ACTIVITY": "イベント", "WEEKLY": "物資調達", "GACHABOX": "補給物資",
		},
		Related:      "関連素材",
		ItemCaption:  "{server}における{name}の作戦別ドロップ統計（ドロップ率の高い順、{total} ステージ中 {shown} 件）",
		StageCaption: "{server}における{stage}のアイテムドロップ統計（ドロップ率の高い順）",
		Definitions: "サンプル数：期間内に報告された作戦の回数。ドロップ数：それらの作戦でドロップしたアイテムの総数。" +
			"ドロップ率 = ドロップ数 ÷ サンプル数（1回あたりの期待ドロップ数で、100% を超えることがあります）。" +
			"１個あたりの必要理性期待値 = 理性 ÷ ドロップ率。期間：作戦のドロップテーブルが最後に変更されてからの期間。",
		SanityFact:           "理性：{sanity}",
		Updated:              "データ更新",
		FullData:             "全データ（JSON）",
		License:              "利用許諾：",
		Stop:                 "。",
		Empty:                "ドロップデータはまだありません。",
		Cold:                 "ドロップデータを計算中です。しばらくしてから再度お試しください。",
		NotFound:             "ページが見つかりません：アイテムまたはステージが存在しないか、このサーバーにはありません。",
		ItemDesc:             "{name}（{server}）のドロップ統計：{highlights}。全 {n} ステージ。",
		StageDesc:            "{stage}（{server}）のドロップ統計：{highlights}。サンプル数 {n}（{window}）。",
		ItemListDesc:         "{server}のアークナイツ素材別ドロップ率：{items} 種類のアイテム、{stages} ステージ。",
		StageListDesc:        "{server}のアークナイツ作戦別ドロップ率：{stages} ステージ、{items} 種類のアイテム。",
		ItemDescNoData:       "{server}のアークナイツ各ステージにおける{name}のドロップ率。",
		StageDescNoData:      "{server}の{stage}のアイテムドロップ率。",
		UpdatedDesc:          "{date} 更新。",
		SanityDesc:           "消費理性 {sanity}。",
		Tagline:              "ペンギン急便データ統計処理部門によるアークナイツのクラウドソーシングドロップデータ。",
		ItemHighlightFirst:   "{code} ドロップ率 {p}（サンプル数 {n}）",
		ItemHighlight:        "{code} {p}（{n}）",
		StageHighlightFirst:  "{item} ドロップ率 {p}",
		StageHighlight:       "{item} {p}",
		HighlightSep:         "、",
		DatasetName:          "{name} ドロップ統計（{server}）",
		VarTimes:             "期間内に報告された作戦の回数",
		VarQuantity:          "それらの作戦でドロップしたアイテムの総数",
		VarRate:              "ドロップ数 ÷ サンプル数（1回あたりの期待ドロップ数で、100% を超えることがあります）",
		MeasurementTechnique: "プレイヤーが報告したゲーム内のドロップ（手動入力と画像認識）",
	},
	"ko": {
		SiteName:        "펭귄 물류 데이터 분석 부서",
		Lang:            "ko",
		OGLocale:        "ko_KR",
		Servers:         map[string]string{"CN": "CN 서버", "US": "US 서버", "JP": "JP 서버", "KR": "KR 서버"},
		ServerLabel:     "서버",
		BreadcrumbLabel: "탐색 경로",
		ByItem:          "아이템별 통계",
		ByStage:         "스테이지별 통계",
		ListCrumb:       "{list} ({server})",
		ItemTitle:       "{name} 드랍률 ({server})",
		StageTitle:      "{stage} 드랍률 ({server})",
		ItemListTitle:   "아이템별 드랍률 ({server})",
		StageListTitle:  "스테이지별 드랍률 ({server})",
		NotFoundTitle:   "페이지를 찾을 수 없습니다",
		ItemHeading:     "{item}의 통계 결과",
		StageHeading:    "{stage}의 통계 결과",
		HStage:          "스테이지", HItem: "아이템", HQuantity: "드랍 수", HTimes: "표본 수", HPercentage: "백분율",
		HAPPPR: "아이템당 필요 이성", HAPCost: "이성", HTimeRange: "집계 기간",
		InBetween: "{0} - {1}",
		ToPresent: "{date} - 현재",
		ItemCategories: map[string]string{
			"MATERIAL": "재료", "CARD_EXP": "작전 기록", "CHIP": "칩", "FURN": "가구", "ACTIVITY_ITEM": "이벤트 아이템",
		},
		ZoneCategories: map[string]string{
			"MAINLINE": "메인", "ACTIVITY_PERMANENT": "에피소드 & 사이드 스토리", "ACTIVITY": "이벤트", "WEEKLY": "물자 비축", "GACHABOX": "보급 물자",
		},
		Related:      "관련 아이템",
		ItemCaption:  "{server} 스테이지별 {name} 드랍 통계 (백분율 높은 순, {total}개 중 {shown}개)",
		StageCaption: "{server} {stage}의 아이템 드랍 통계 (백분율 높은 순)",
		Definitions: "표본 수: 집계 기간 동안 보고된 스테이지 클리어 횟수. 드랍 수: 그 클리어들에서 드랍된 아이템의 총수. " +
			"백분율 = 드랍 수 ÷ 표본 수 (1회당 기대 드랍 수로, 100%를 넘을 수 있습니다). " +
			"아이템당 필요 이성 = 이성 ÷ 백분율. 집계 기간: 스테이지의 드랍 테이블이 마지막으로 바뀐 이후의 기간.",
		SanityFact:           "이성: {sanity}",
		Updated:              "데이터 업데이트",
		FullData:             "전체 데이터 (JSON)",
		License:              "라이센스: ",
		Stop:                 ". ",
		Empty:                "아직 드랍 데이터가 없습니다.",
		Cold:                 "드랍 데이터를 계산하는 중입니다. 잠시 후 다시 확인해 주세요.",
		NotFound:             "페이지를 찾을 수 없습니다: 아이템이나 스테이지가 없거나 이 서버에 없습니다.",
		ItemDesc:             "{name} ({server}) 드랍 통계: {highlights}. 총 {n}개 스테이지. ",
		StageDesc:            "{stage} ({server}) 드랍 통계: {highlights}. 표본 수 {n} ({window}). ",
		ItemListDesc:         "{server} 명일방주 아이템별 드랍률: 아이템 {items}종, 스테이지 {stages}개. ",
		StageListDesc:        "{server} 명일방주 스테이지별 드랍률: 스테이지 {stages}개, 아이템 {items}종. ",
		ItemDescNoData:       "{server} 명일방주 스테이지별 {name} 드랍률. ",
		StageDescNoData:      "{server} {stage}의 아이템 드랍률. ",
		UpdatedDesc:          "{date} 업데이트. ",
		SanityDesc:           "이성 소모 {sanity}. ",
		Tagline:              "펭귄 물류 데이터 분석 부서의 명일방주 크라우드소싱 드랍 데이터.",
		ItemHighlightFirst:   "{code} 백분율 {p} (표본 수 {n})",
		ItemHighlight:        "{code} {p} ({n})",
		StageHighlightFirst:  "{item} 백분율 {p}",
		StageHighlight:       "{item} {p}",
		HighlightSep:         ", ",
		DatasetName:          "{name} 드랍 통계 ({server})",
		VarTimes:             "집계 기간 동안 보고된 스테이지 클리어 횟수",
		VarQuantity:          "그 클리어들에서 드랍된 아이템의 총수",
		VarRate:              "드랍 수 ÷ 표본 수 (1회당 기대 드랍 수로, 100%를 넘을 수 있습니다)",
		MeasurementTechnique: "플레이어가 제출한 게임 내 드랍 보고 (수동 입력 및 스크린샷 인식)",
	},
}

// fill replaces each {key} of pattern with its value in kv (key, value, key, value, ...)
func fill(pattern string, kv ...string) string {
	return strings.NewReplacer(braced(kv)...).Replace(pattern)
}

func braced(kv []string) []string {
	out := make([]string, len(kv))
	for i := 0; i < len(kv); i += 2 {
		out[i] = "{" + kv[i] + "}"
		out[i+1] = kv[i+1]
	}
	return out
}
