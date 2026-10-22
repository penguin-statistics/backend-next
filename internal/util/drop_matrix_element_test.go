package util

import (
	"reflect"
	"testing"

	"exusiai.dev/backend-next/internal/model"
)

func TestGetDropMatrixElementsMap(t *testing.T) {
	a := &model.DropMatrixElement{StageID: 1, ItemID: 2, RangeID: 3, DayNum: 4}
	b := &model.DropMatrixElement{StageID: 1, ItemID: 2, RangeID: 5, DayNum: 4}
	c := &model.DropMatrixElement{StageID: 6, ItemID: 2, RangeID: 3, DayNum: 7}
	elements := []*model.DropMatrixElement{a, b, c}

	byRange := map[int]map[int]map[int]*model.DropMatrixElement{1: {2: {3: a, 5: b}}, 6: {2: {3: c}}}
	if got := GetDropMatrixElementsMap(elements, true); !reflect.DeepEqual(got, byRange) {
		t.Errorf("by range id %v, want %v", got, byRange)
	}
	// the last of the elements with the same ids is kept
	byDay := map[int]map[int]map[int]*model.DropMatrixElement{1: {2: {4: b}}, 6: {2: {7: c}}}
	if got := GetDropMatrixElementsMap(elements, false); !reflect.DeepEqual(got, byDay) {
		t.Errorf("by day %v, want %v", got, byDay)
	}
}
