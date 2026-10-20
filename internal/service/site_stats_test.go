package service

import (
	"testing"

	"exusiai.dev/gommon/constant"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
)

func TestMergeSiteStatsAggregates(t *testing.T) {
	base := []*model.SiteStatsAggregate{
		{StageID: 1, ItemID: 10, Times: null.IntFrom(5), Quantity: 7},
		{StageID: 1, ItemID: 11, Quantity: 3}, // only times = 0 elements
	}
	recent := []*model.SiteStatsAggregate{
		{StageID: 1, ItemID: 10, Times: null.IntFrom(2), Quantity: 1},
		{StageID: 1, ItemID: 11, Times: null.IntFrom(4), Quantity: 2},
		{StageID: 2, ItemID: 10, Quantity: 9},
	}
	merged := mergeSiteStatsAggregates(base, recent)

	expected := map[[2]int]model.SiteStatsAggregate{
		{1, 10}: {StageID: 1, ItemID: 10, Times: null.IntFrom(7), Quantity: 8},
		{1, 11}: {StageID: 1, ItemID: 11, Times: null.IntFrom(4), Quantity: 5},
		{2, 10}: {StageID: 2, ItemID: 10, Quantity: 9},
	}
	if len(merged) != len(expected) {
		t.Fatalf("got %d aggregates, want %d", len(merged), len(expected))
	}
	for _, aggregate := range merged {
		if *aggregate != expected[[2]int{aggregate.StageID, aggregate.ItemID}] {
			t.Errorf("got %+v", *aggregate)
		}
	}
	// the cached base aggregates must not change
	if base[0].Times.Int64 != 5 || base[0].Quantity != 7 || base[1].Times.Valid {
		t.Errorf("base aggregates were modified: %+v %+v", *base[0], *base[1])
	}
}

func TestCalcStageTimesForShimSiteStats(t *testing.T) {
	stagesMap := map[int]*model.Stage{
		1: {StageID: 1, ArkStageID: "main_01-07", Sanity: null.IntFrom(6)},
		2: {StageID: 2, ArkStageID: constant.RecruitStageID},
		3: {StageID: 3, ArkStageID: "gacha_01", ExtraProcessType: null.StringFrom("GACHABOX")},
		4: {StageID: 4, ArkStageID: "act_01"}, // no sanity
	}
	aggregates := []*model.SiteStatsAggregate{
		{StageID: 1, ItemID: 10, Times: null.IntFrom(5)},
		{StageID: 1, ItemID: 11, Times: null.IntFrom(9)},
		{StageID: 1, ItemID: 12}, // no times > 0: ignored
		{StageID: 2, ItemID: 10, Times: null.IntFrom(100)},
		{StageID: 3, ItemID: 10, Times: null.IntFrom(100)},
		{StageID: 4, ItemID: 10, Times: null.IntFrom(3)},
		{StageID: 5, ItemID: 10, Times: null.IntFrom(100)}, // unknown stage
	}
	stageTimes, sanity := calcStageTimesForShimSiteStats(aggregates, stagesMap)

	got := map[string]int{}
	for _, stageTime := range stageTimes {
		got[stageTime.StageID] = stageTime.Times
	}
	want := map[string]int{"main_01-07": 9, "act_01": 3}
	if len(got) != len(want) || got["main_01-07"] != 9 || got["act_01"] != 3 {
		t.Errorf("got stage times %v, want %v", got, want)
	}
	if sanity != 6*9 {
		t.Errorf("got sanity %d, want %d", sanity, 6*9)
	}
}

func TestCalcItemQuantitiesForShimSiteStats(t *testing.T) {
	itemsMap := map[int]*model.Item{
		10: {ItemID: 10, ArkItemID: "30012", Type: constant.ItemTypeMaterial},
		11: {ItemID: 11, ArkItemID: "furni", Type: constant.ItemTypeFurniture},
		12: {ItemID: 12, ArkItemID: "card", Type: "CARD_EXP"},
	}
	aggregates := []*model.SiteStatsAggregate{
		{StageID: 1, ItemID: 10, Quantity: 4},
		{StageID: 2, ItemID: 10, Quantity: 6},
		{StageID: 1, ItemID: 11, Quantity: 0},
		{StageID: 1, ItemID: 12, Quantity: 5},
		{StageID: 1, ItemID: 13, Quantity: 5}, // unknown item
	}
	got := map[string]int{}
	for _, itemQuantity := range calcItemQuantitiesForShimSiteStats(aggregates, itemsMap) {
		got[itemQuantity.ItemID] = itemQuantity.Quantity
	}
	want := map[string]int{"30012": 10, "furni": 0}
	if len(got) != len(want) || got["30012"] != 10 || got["furni"] != 0 {
		t.Errorf("got item quantities %v, want %v", got, want)
	}
}
