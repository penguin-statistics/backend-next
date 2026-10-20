package service

import (
	"testing"

	"exusiai.dev/backend-next/internal/model"
)

func TestCalcTotalTimesFromPatternQuantities(t *testing.T) {
	quantityResults := []*model.TotalQuantityResultForPatternMatrix{
		{StageID: 1, PatternID: 100, TotalQuantity: 3},
		{StageID: 2, PatternID: 100, TotalQuantity: 4},
		{StageID: 1, PatternID: 101, TotalQuantity: 5},
	}
	got := map[int]int{}
	for _, result := range calcTotalTimesFromPatternQuantities(quantityResults) {
		if _, ok := got[result.StageID]; ok {
			t.Fatalf("stage %d reported twice", result.StageID)
		}
		got[result.StageID] = result.TotalTimes
	}
	if len(got) != 2 || got[1] != 8 || got[2] != 4 {
		t.Errorf("got %v, want map[1:8 2:4]", got)
	}
}
