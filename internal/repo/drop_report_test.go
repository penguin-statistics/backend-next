package repo

import (
	"testing"

	"exusiai.dev/gommon/constant"
)

func TestInSourceCategory(t *testing.T) {
	for _, tc := range []struct {
		sourceCategory string
		isManual       bool
		want           bool
	}{
		{constant.SourceCategoryAll, true, true},
		{constant.SourceCategoryAll, false, true},
		{constant.SourceCategoryManual, true, true},
		{constant.SourceCategoryManual, false, false},
		{constant.SourceCategoryAutomated, true, false},
		{constant.SourceCategoryAutomated, false, true},
	} {
		if got := inSourceCategory(tc.sourceCategory, tc.isManual); got != tc.want {
			t.Errorf("inSourceCategory(%q, %v) = %v, want %v", tc.sourceCategory, tc.isManual, got, tc.want)
		}
	}
}

func TestSplitDropMatrixRowsBySourceCategories(t *testing.T) {
	timesRows := []dropMatrixTimesRow{
		{StageID: 1, IsManual: true, TotalTimes: 3},
		{StageID: 1, IsManual: false, TotalTimes: 5},
		{StageID: 1, IsManual: false, TotalTimes: 1}, // another segment of the same stage
		{StageID: 2, IsManual: true, TotalTimes: 2},
	}
	quantityRows := []dropMatrixQuantityRow{
		{StageID: 1, ItemID: 10, Quantity: 1, IsManual: true, Count: 2},
		{StageID: 1, ItemID: 10, Quantity: 1, IsManual: false, Count: 4},
		{StageID: 1, ItemID: 10, Quantity: 2, IsManual: false, Count: 1},
	}
	categories := []string{constant.SourceCategoryAll, constant.SourceCategoryManual, constant.SourceCategoryAutomated}
	times, quantities := splitDropMatrixRowsBySourceCategories(timesRows, quantityRows, categories)

	wantTimes := map[string]map[int]int{
		constant.SourceCategoryAll:       {1: 9, 2: 2},
		constant.SourceCategoryManual:    {1: 3, 2: 2},
		constant.SourceCategoryAutomated: {1: 6},
	}
	for category, want := range wantTimes {
		got := map[int]int{}
		for _, result := range times[category] {
			got[result.StageID] = result.TotalTimes
		}
		if len(got) != len(want) {
			t.Errorf("%s: got times %v, want %v", category, got, want)
		}
		for stageId, totalTimes := range want {
			if got[stageId] != totalTimes {
				t.Errorf("%s: got times %v, want %v", category, got, want)
			}
		}
	}

	wantCounts := map[string]map[int]int{ // quantity -> count, all for stage 1 item 10
		constant.SourceCategoryAll:       {1: 6, 2: 1},
		constant.SourceCategoryManual:    {1: 2},
		constant.SourceCategoryAutomated: {1: 4, 2: 1},
	}
	for category, want := range wantCounts {
		got := map[int]int{}
		for _, result := range quantities[category] {
			got[result.Quantity] = result.Count
		}
		if len(got) != len(want) {
			t.Errorf("%s: got counts %v, want %v", category, got, want)
		}
		for quantity, count := range want {
			if got[quantity] != count {
				t.Errorf("%s: got counts %v, want %v", category, got, want)
			}
		}
	}
}
