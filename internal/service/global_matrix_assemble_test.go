package service

import (
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
)

// A target whose times are all in the days before a split and whose quantity is all in those after is emitted from
// the merged aggregate, though from neither side alone: the emission is decided after the merge.
func TestAssembleGlobalDropMatricesAfterAMerge(t *testing.T) {
	start := time.Unix(1700000000, 0)
	end := start.Add(30 * 24 * time.Hour)
	targets := []*model.GlobalDropMatrixTarget{
		{StageID: 1, ItemID: 10, TimeRange: &model.TimeRange{StartTime: &start, EndTime: &end}},
		{StageID: 2, ItemID: 10, TimeRange: &model.TimeRange{StartTime: &start, EndTime: &end}},
	}
	sourceCategories := []string{constant.SourceCategoryAll}
	key := model.GlobalDropMatrixKey{SourceCategory: constant.SourceCategoryAll, StageID: 1, ItemID: 10}
	before := map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate{
		key: {Times: null.IntFrom(10), QuantityBuckets: map[int]int{}},
	}
	after := map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate{
		key: {Quantity: null.IntFrom(4), QuantityBuckets: map[int]int{1: 2, 2: 1}},
	}
	for name, aggregates := range map[string]map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate{"before": before, "after": after} {
		if matrix := assembleGlobalDropMatrices(targets, aggregates, sourceCategories)[constant.SourceCategoryAll].Matrix; len(matrix) != 0 {
			t.Fatalf("%s alone: %d elements, want none", name, len(matrix))
		}
	}

	merged := &model.GlobalDropMatrixAggregate{}
	merged.Add(before[key])
	merged.Add(after[key])
	matrix := assembleGlobalDropMatrices(targets, map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate{key: merged}, sourceCategories)[constant.SourceCategoryAll].Matrix
	if len(matrix) != 1 {
		t.Fatalf("merged: %d elements, want 1", len(matrix))
	}
	if el := matrix[0]; el.StageID != 1 || el.ItemID != 10 || el.Times != 10 || el.Quantity != 4 || el.TimeRange != targets[0].TimeRange || el.StdDev <= 0 {
		t.Fatalf("merged: %+v", el)
	}
}

// The same for a pattern matrix target, whose quantities without times are an error, also when the times are in
// the other side.
func TestAssembleGlobalPatternMatricesAfterAMerge(t *testing.T) {
	start := time.Unix(1700000000, 0)
	end := start.Add(30 * 24 * time.Hour)
	targets := []*model.GlobalPatternMatrixTarget{{StageID: 1, TimeRange: &model.TimeRange{StartTime: &start, EndTime: &end}}}
	sourceCategories := []string{constant.SourceCategoryAll}
	before := &model.GlobalPatternMatrixAggregates{TimesByStageID: map[int]int{1: 10}, QuantitiesByStageID: map[int]map[int]int{}}
	after := &model.GlobalPatternMatrixAggregates{TimesByStageID: map[int]int{}, QuantitiesByStageID: map[int]map[int]int{1: {7: 3, 8: 1}}}

	results, err := assembleGlobalPatternMatrices("CN", targets, map[string]*model.GlobalPatternMatrixAggregates{constant.SourceCategoryAll: before}, sourceCategories)
	if err != nil || len(results[constant.SourceCategoryAll].PatternMatrix) != 0 {
		t.Fatalf("before alone: %v, %v", results, err)
	}
	if _, err := assembleGlobalPatternMatrices("CN", targets, map[string]*model.GlobalPatternMatrixAggregates{constant.SourceCategoryAll: after}, sourceCategories); err == nil {
		t.Fatal("after alone: quantities without times assembled")
	}

	merged := &model.GlobalPatternMatrixAggregates{}
	merged.Add(before)
	merged.Add(after)
	results, err = assembleGlobalPatternMatrices("CN", targets, map[string]*model.GlobalPatternMatrixAggregates{constant.SourceCategoryAll: merged}, sourceCategories)
	if err != nil {
		t.Fatal(err)
	}
	matrix := results[constant.SourceCategoryAll].PatternMatrix
	if len(matrix) != 2 {
		t.Fatalf("merged: %d elements, want 2", len(matrix))
	}
	for _, el := range matrix {
		if el.StageID != 1 || el.Times != 10 || el.Quantity != after.QuantitiesByStageID[1][el.PatternID] {
			t.Fatalf("merged: %+v", el)
		}
	}
}
