package service

import (
	"context"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/util"
)

func TestCalcDropMatrix(t *testing.T) {
	start := time.Date(2026, 10, 19, 4, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	filter := map[int][]int{1: {10, 11, 12}, 2: {10}, 3: {10}}
	queryCtx := &model.DropReportQueryContext{
		Server:          "CN",
		StartTime:       &start,
		EndTime:         &end,
		StageItemFilter: &filter,
		SourceCategory:  constant.SourceCategoryManual,
	}
	timesResults := []*model.TotalTimesResult{
		{StageID: 1, TotalTimes: 10},
		{StageID: 2, TotalTimes: 4},
		{StageID: 3, TotalTimes: 7}, // reported, but nothing dropped
	}
	quantityUniqCountResults := []*model.QuantityUniqCountResultForDropMatrix{
		{StageID: 1, ItemID: 10, Quantity: 1, Count: 3},
		{StageID: 1, ItemID: 10, Quantity: 2, Count: 1},
		{StageID: 1, ItemID: 99, Quantity: 1, Count: 2}, // dropped, though not in the drop infos
		{StageID: 2, ItemID: 10, Quantity: 3, Count: 4},
	}
	// the dropped items of each stage known from its drop infos, as an earlier source category looked them up
	dropItemIdsByStageId := map[int][]int{1: {10, 11}, 2: {10}, 3: {10}}

	elements, err := (&DropMatrix{}).calcDropMatrix(context.Background(), queryCtx, timesResults, quantityUniqCountResults, dropItemIdsByStageId)
	if err != nil {
		t.Fatal(err)
	}

	type key struct{ stageId, itemId int }
	want := map[key]struct {
		quantity, times int
		buckets         map[int]int
	}{
		{1, 10}: {5, 10, map[int]int{1: 3, 2: 1}},
		{1, 99}: {2, 10, map[int]int{1: 2}},
		{1, 11}: {0, 10, map[int]int{0: 10}}, // in the drop infos, never dropped
		{2, 10}: {12, 4, map[int]int{3: 4}},
	}
	if len(elements) != len(want) {
		t.Fatalf("got %d elements, want %d", len(elements), len(want))
	}
	dayNum := util.GetDayNum(&start, "CN")
	for _, el := range elements {
		w, ok := want[key{el.StageID, el.ItemID}]
		if !ok {
			t.Fatalf("unexpected element of stage %d item %d", el.StageID, el.ItemID)
		}
		if el.Quantity != w.quantity || el.Times != w.times || len(el.QuantityBuckets) != len(w.buckets) {
			t.Errorf("stage %d item %d: got quantity %d times %d buckets %v, want %d %d %v",
				el.StageID, el.ItemID, el.Quantity, el.Times, el.QuantityBuckets, w.quantity, w.times, w.buckets)
		}
		for quantity, count := range w.buckets {
			if el.QuantityBuckets[quantity] != count {
				t.Errorf("stage %d item %d: got buckets %v, want %v", el.StageID, el.ItemID, el.QuantityBuckets, w.buckets)
			}
		}
		if el.Server != "CN" || el.SourceCategory != constant.SourceCategoryManual || el.DayNum != dayNum ||
			!el.StartTime.Equal(start) || !el.EndTime.Equal(end) {
			t.Errorf("stage %d item %d: got %s %s day %d %v-%v", el.StageID, el.ItemID, el.Server, el.SourceCategory, el.DayNum, el.StartTime, el.EndTime)
		}
	}
	// the cached dropped items are reused, not replaced
	if len(dropItemIdsByStageId) != 3 || len(dropItemIdsByStageId[1]) != 2 {
		t.Errorf("dropped item ids changed: %v", dropItemIdsByStageId)
	}
}

func TestCalcTotalQuantityForDropMatrix(t *testing.T) {
	results := calcTotalQuantityForDropMatrix([]*model.QuantityUniqCountResultForDropMatrix{
		{StageID: 1, ItemID: 10, Quantity: 1, Count: 3},
		{StageID: 1, ItemID: 10, Quantity: 4, Count: 2},
		{StageID: 1, ItemID: 11, Quantity: 2, Count: 5},
		{StageID: 2, ItemID: 10, Quantity: 1, Count: 1},
	})
	got := map[[2]int]int{}
	for _, result := range results {
		got[[2]int{result.StageID, result.ItemID}] = result.TotalQuantity
	}
	want := map[[2]int]int{{1, 10}: 11, {1, 11}: 10, {2, 10}: 1}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
