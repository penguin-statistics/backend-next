package reportutil

import (
	"exusiai.dev/backend-next/internal/model/types"
)

// mergeDropsLinearMax is the number of merged drops up to which a merge looks a drop up by scanning them: a report has
// a handful of drops, for which a scan is cheaper than a map, but a request may carry many.
const mergeDropsLinearMax = 16

type dropTypeAndItemID struct {
	dropType string
	itemID   int
}

// MergeDropsByDropTypeAndItemID merges drops with same (DropType, ItemID) pair into one drop, summing up their Quantity values.
// The first drop of each pair is kept, in place, with the Quantity of the others added to it; the merged drops are in
// the order their pairs first appear, and nil for no drops.
func MergeDropsByDropTypeAndItemID(drops []*types.Drop) (mergedDrops []*types.Drop) {
	var index map[dropTypeAndItemID]*types.Drop
	for _, drop := range drops {
		var merged *types.Drop
		if index != nil {
			merged = index[dropTypeAndItemID{drop.DropType, drop.ItemID}]
		} else {
			for _, m := range mergedDrops {
				if m.ItemID == drop.ItemID && m.DropType == drop.DropType {
					merged = m
					break
				}
			}
		}
		if merged != nil {
			merged.Quantity += drop.Quantity
			continue
		}
		mergedDrops = append(mergedDrops, drop)
		if index != nil {
			index[dropTypeAndItemID{drop.DropType, drop.ItemID}] = drop
		} else if len(mergedDrops) > mergeDropsLinearMax {
			index = make(map[dropTypeAndItemID]*types.Drop, len(drops))
			for _, m := range mergedDrops {
				index[dropTypeAndItemID{m.DropType, m.ItemID}] = m
			}
		}
	}
	return mergedDrops
}

// MergeDrops merges drops with same ItemID pair into one drop, summing up their Quantity values.
// The first drop of each item is kept, in place, with the Quantity of the others added to it.
func MergeDropsByItemID(drops []*types.Drop) (mergedDrops []*types.Drop) {
	for _, drop := range drops {
		merged := false
		for _, m := range mergedDrops {
			if m.ItemID == drop.ItemID {
				m.Quantity += drop.Quantity
				merged = true
				break
			}
		}
		if !merged {
			mergedDrops = append(mergedDrops, drop)
		}
	}
	return mergedDrops
}

// AggregateGachaBoxDrops sets the Times of a gacha box report to the sum of the Quantity of its drops, 0 for no drops.
func AggregateGachaBoxDrops(report *types.ReportTaskSingleReport) {
	times := 0
	for _, drop := range report.Drops {
		times += drop.Quantity
	}
	report.Times = times
}
