package main

import (
	"testing"

	"github.com/davecgh/go-spew/spew"

	"github.com/penguin-statistics/backend-next/internal/model/types"
	"github.com/penguin-statistics/backend-next/internal/util/reportutil"
)

type MergeDropsTestCase struct {
	drops       []types.Drop
	mergedDrops []types.Drop
}

func pointerify(drops []types.Drop) []*types.Drop {
	var ret []*types.Drop
	for _, drop := range drops {
		var copiedDrop types.Drop
		copiedDrop = drop
		d := &copiedDrop
		ret = append(ret, d)
	}
	return ret
}

func TestMergeDrops(t *testing.T) {
	testCases := []MergeDropsTestCase{
		{
			[]types.Drop{
				{"a", 1, 1},
				{"b", 1, 1},
				{"c", 2, 1},
			},
			[]types.Drop{
				{"a", 1, 2},
				{"b", 2, 1},
			},
		},
		{
			[]types.Drop{
				{"a", 1, 1},
				{"b", 2, 1},
				{"c", 2, 1},
			},
			[]types.Drop{
				{"b", 1, 1},
				{"c", 2, 2},
			},
		},
	}

	for _, testCase := range testCases {
		mergedDrops := reportutil.MergeDropsByItemID(pointerify(testCase.drops))
		if len(mergedDrops) != len(testCase.mergedDrops) {
			t.Errorf("MergeDropsByItemID(%v) = %v, want %v", spew.Sdump(testCase.drops), spew.Sdump(mergedDrops), spew.Sdump(testCase.mergedDrops))
		}
		for i := 0; i < len(mergedDrops); i++ {
			if mergedDrops[i].ItemID != testCase.mergedDrops[i].ItemID {
				t.Errorf("MergeDropsByItemID(%v) = %v, want %v", spew.Sdump(testCase.drops), spew.Sdump(mergedDrops), spew.Sdump(testCase.mergedDrops))
			}
			if mergedDrops[i].Quantity != testCase.mergedDrops[i].Quantity {
				t.Errorf("MergeDropsByItemID(%v) = %v, want %v", spew.Sdump(testCase.drops), spew.Sdump(mergedDrops), spew.Sdump(testCase.mergedDrops))
			}
		}
	}
}
