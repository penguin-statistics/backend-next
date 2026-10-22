package reportutil

import (
	"fmt"
	"reflect"
	"testing"

	"exusiai.dev/backend-next/internal/model/types"
)

// mergeCase is a merge of drops: want gives each merged drop, in order, as "dropType/itemId:quantity@index", where
// index is the position of its pointer in the input, and after is the input after the merge (the merged drops are
// the first of their kind, in place, with the quantities of the others added to them).
type mergeCase struct {
	name  string
	drops []types.Drop // nil for a nil input
	want  []string     // nil for a nil result
	after []types.Drop
}

func runMergeCases(t *testing.T, merge func([]*types.Drop) []*types.Drop, cases []mergeCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var input []*types.Drop
			if tc.drops != nil {
				input = make([]*types.Drop, len(tc.drops))
				for i := range tc.drops {
					d := tc.drops[i]
					input[i] = &d
				}
			}
			merged := merge(input)

			var got []string
			for _, m := range merged {
				index := -1
				for i, d := range input {
					if d == m {
						index = i
					}
				}
				got = append(got, fmt.Sprintf("%s/%d:%d@%d", m.DropType, m.ItemID, m.Quantity, index))
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("merged into %#v, want %#v", got, tc.want)
			}
			after := make([]types.Drop, 0, len(input))
			for _, d := range input {
				after = append(after, *d)
			}
			if tc.after == nil {
				tc.after = []types.Drop{}
			}
			if !reflect.DeepEqual(after, tc.after) {
				t.Errorf("input after the merge %v, want %v", after, tc.after)
			}
		})
	}
}

func regular(itemId, quantity int) types.Drop {
	return types.Drop{DropType: "REGULAR", ItemID: itemId, Quantity: quantity}
}

func extra(itemId, quantity int) types.Drop {
	return types.Drop{DropType: "EXTRA", ItemID: itemId, Quantity: quantity}
}

func TestMergeDropsByItemID(t *testing.T) {
	runMergeCases(t, MergeDropsByItemID, []mergeCase{
		{name: "nil"},
		{name: "empty", drops: []types.Drop{}},
		{
			name:  "one",
			drops: []types.Drop{regular(30012, 2)},
			want:  []string{"REGULAR/30012:2@0"},
			after: []types.Drop{regular(30012, 2)},
		},
		{
			name:  "unique",
			drops: []types.Drop{regular(30012, 2), regular(30011, 1), regular(3003, 5)},
			want:  []string{"REGULAR/30012:2@0", "REGULAR/30011:1@1", "REGULAR/3003:5@2"},
			after: []types.Drop{regular(30012, 2), regular(30011, 1), regular(3003, 5)},
		},
		{
			name:  "duplicates",
			drops: []types.Drop{regular(30012, 2), regular(30011, 1), regular(30012, 3), regular(3003, 1), regular(30011, 4)},
			want:  []string{"REGULAR/30012:5@0", "REGULAR/30011:5@1", "REGULAR/3003:1@3"},
			after: []types.Drop{regular(30012, 5), regular(30011, 5), regular(30012, 3), regular(3003, 1), regular(30011, 4)},
		},
		{
			name:  "across drop types",
			drops: []types.Drop{regular(1, 1), extra(2, 1), extra(1, 2), regular(3, 0), regular(2, 2)},
			want:  []string{"REGULAR/1:3@0", "EXTRA/2:3@1", "REGULAR/3:0@3"},
			after: []types.Drop{regular(1, 3), extra(2, 3), extra(1, 2), regular(3, 0), regular(2, 2)},
		},
	})
}

func TestMergeDropsByDropTypeAndItemID(t *testing.T) {
	cases := []mergeCase{
		{name: "nil"},
		{name: "empty", drops: []types.Drop{}},
		{
			name:  "one",
			drops: []types.Drop{regular(30012, 2)},
			want:  []string{"REGULAR/30012:2@0"},
			after: []types.Drop{regular(30012, 2)},
		},
		{
			name:  "duplicates",
			drops: []types.Drop{regular(30012, 2), regular(30011, 1), regular(30012, 3), regular(3003, 1), regular(30011, 4)},
			want:  []string{"REGULAR/30012:5@0", "REGULAR/30011:5@1", "REGULAR/3003:1@3"},
			after: []types.Drop{regular(30012, 5), regular(30011, 5), regular(30012, 3), regular(3003, 1), regular(30011, 4)},
		},
		{
			name:  "across drop types",
			drops: []types.Drop{regular(1, 1), extra(2, 1), extra(1, 2), regular(3, 0), regular(2, 2), extra(1, 1)},
			want:  []string{"REGULAR/1:1@0", "EXTRA/2:1@1", "EXTRA/1:3@2", "REGULAR/3:0@3", "REGULAR/2:2@4"},
			after: []types.Drop{regular(1, 1), extra(2, 1), extra(1, 3), regular(3, 0), regular(2, 2), extra(1, 1)},
		},
	}

	// more kinds of drops than a scan handles: they are looked up in a map, with the same results
	var many mergeCase
	many.name = "many kinds"
	for round := 0; round < 3; round++ {
		for itemId := 0; itemId < 2*mergeDropsLinearMax; itemId++ {
			many.drops = append(many.drops, regular(itemId, round+1), extra(itemId, 10*(round+1)))
		}
	}
	for itemId := 0; itemId < 2*mergeDropsLinearMax; itemId++ {
		many.want = append(many.want, fmt.Sprintf("REGULAR/%d:6@%d", itemId, 2*itemId), fmt.Sprintf("EXTRA/%d:60@%d", itemId, 2*itemId+1))
	}
	many.after = append([]types.Drop(nil), many.drops...)
	for i := 0; i < 4*mergeDropsLinearMax; i++ {
		many.after[i].Quantity *= 6
	}
	cases = append(cases, many)

	runMergeCases(t, MergeDropsByDropTypeAndItemID, cases)
}

func TestAggregateGachaBoxDrops(t *testing.T) {
	for _, tc := range []struct {
		name  string
		drops []*types.Drop
		want  int
	}{
		{"nil", nil, 0},
		{"empty", []*types.Drop{}, 0},
		{"drops", []*types.Drop{{ItemID: 1, Quantity: 3}, {ItemID: 2, Quantity: 0}, {ItemID: 1, Quantity: 4}}, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := &types.ReportTaskSingleReport{Drops: tc.drops, Times: 99}
			AggregateGachaBoxDrops(report)
			if report.Times != tc.want {
				t.Errorf("times %d, want %d", report.Times, tc.want)
			}
		})
	}
}
