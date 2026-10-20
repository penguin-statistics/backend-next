package service

import (
	"math/rand"
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

func TestInterceptPatternMatrixResultsKeepsTheMostFrequentPatterns(t *testing.T) {
	// the times of a stage are the same for all its patterns
	elements := []*model.OnePatternMatrixElement{
		{StageID: 1, PatternID: 1, Times: 100, Quantity: 5},
		{StageID: 1, PatternID: 2, Times: 100, Quantity: 40},
		{StageID: 1, PatternID: 3, Times: 100, Quantity: 7},
		{StageID: 1, PatternID: 4, Times: 100, Quantity: 40},
		{StageID: 1, PatternID: 5, Times: 100, Quantity: 8},
		{StageID: 2, PatternID: 1, Times: 10, Quantity: 1},
	}
	for i := 0; i < 20; i++ {
		input := append([]*model.OnePatternMatrixElement(nil), elements...)
		rand.Shuffle(len(input), func(i, j int) { input[i], input[j] = input[j], input[i] })
		kept, err := (&PatternMatrix{}).interceptPatternMatrixResults(input, 3)
		if err != nil {
			t.Fatal(err)
		}
		got := map[[2]int]bool{}
		for _, el := range kept {
			got[[2]int{el.StageID, el.PatternID}] = true
		}
		// pattern 2 and 4 tie: both are kept, then the next most frequent
		want := map[[2]int]bool{{1, 2}: true, {1, 4}: true, {1, 5}: true, {2, 1}: true}
		if len(got) != len(want) {
			t.Fatalf("kept %v, want %v", got, want)
		}
		for k := range want {
			if !got[k] {
				t.Fatalf("kept %v, want %v", got, want)
			}
		}
	}
}
