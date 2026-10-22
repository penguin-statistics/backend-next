package reportutil

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/ahmetb/go-linq/v3"

	"exusiai.dev/backend-next/internal/model/types"
)

// linqMergeDropsByItemID is MergeDropsByItemID as it was with go-linq.
func linqMergeDropsByItemID(drops []*types.Drop) (mergedDrops []*types.Drop) {
	linq.
		From(drops).
		GroupByT(func(drop *types.Drop) int {
			return drop.ItemID
		}, func(drop *types.Drop) *types.Drop {
			return drop
		}).
		SelectT(func(group linq.Group) *types.Drop {
			return linq.From(group.Group).
				AggregateT(func(drop *types.Drop, next *types.Drop) *types.Drop {
					drop.Quantity += next.Quantity
					return drop
				}).(*types.Drop)
		}).
		ToSlice(&mergedDrops)
	return mergedDrops
}

func TestMergeDropsByItemIDAsLinq(t *testing.T) {
	type drop struct{ itemID, quantity int }
	for _, tc := range []struct {
		name  string
		drops []drop
	}{
		{"nil", nil},
		{"empty", []drop{}},
		{"one", []drop{{30012, 2}}},
		{"unique", []drop{{30012, 2}, {30011, 1}, {3003, 5}, {30061, 1}}},
		{"duplicates", []drop{{30012, 2}, {30011, 1}, {30012, 3}, {3003, 1}, {30011, 4}}},
		{"three of an item", []drop{{30012, 1}, {30012, 2}, {30012, 3}, {30011, 1}}},
		{"mixed", []drop{{1, 1}, {2, 1}, {1, 1}, {3, 0}, {2, 2}, {3, 1}, {1, 5}, {4, 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// newDrops returns the drops of the case, and their pointers
			newDrops := func() ([]*types.Drop, []*types.Drop) {
				if tc.drops == nil {
					return nil, nil
				}
				drops := make([]*types.Drop, len(tc.drops))
				for i, d := range tc.drops {
					drops[i] = &types.Drop{DropType: "NORMAL_DROP", ItemID: d.itemID, Quantity: d.quantity}
				}
				return drops, append([]*types.Drop(nil), drops...)
			}
			// describe returns the merged drops sorted, each with the index of its pointer in the input, and the input
			// after the merge
			describe := func(merged, input []*types.Drop) ([]string, []types.Drop) {
				var out []string
				for _, m := range merged {
					index := -1
					for i, d := range input {
						if d == m {
							index = i
						}
					}
					out = append(out, fmt.Sprintf("%d:%d@%d", m.ItemID, m.Quantity, index))
				}
				sort.Strings(out)
				after := make([]types.Drop, len(input))
				for i, d := range input {
					after[i] = *d
				}
				return out, after
			}

			wantDrops, wantInput := newDrops()
			want := linqMergeDropsByItemID(wantDrops)
			gotDrops, gotInput := newDrops()
			got := MergeDropsByItemID(gotDrops)

			if (got == nil) != (want == nil) || len(got) != len(want) {
				t.Fatalf("got %d drops (nil %v), want %d (nil %v)", len(got), got == nil, len(want), want == nil)
			}
			gotMerged, gotAfter := describe(got, gotInput)
			wantMerged, wantAfter := describe(want, wantInput)
			if !reflect.DeepEqual(gotMerged, wantMerged) {
				t.Errorf("merged into %v, want %v", gotMerged, wantMerged)
			}
			if !reflect.DeepEqual(gotAfter, wantAfter) {
				t.Errorf("input after the merge %v, want %v", gotAfter, wantAfter)
			}
		})
	}
}
