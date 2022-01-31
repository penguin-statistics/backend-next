package reportutils

import (
	"github.com/ahmetb/go-linq/v3"

	"github.com/penguin-statistics/backend-next/internal/models/types"
)

// MergeDrops merges drops with same (DropType, ItemID) pair into one drop, summing up their Quantity values.
func MergeDrops(drops []types.Drop) (mergedDrops []types.Drop) {
	linq.
		From(drops).
		GroupByT(func(drop types.Drop) string {
			return drop.DropType + drop.ItemID
		}, func(drop types.Drop) types.Drop {
			return drop
		}).
		SelectT(func(group linq.Group) types.Drop {
			return linq.From(group.Group).
				AggregateT(func(drop types.Drop, next types.Drop) types.Drop {
					drop.Quantity += next.Quantity
					return drop
				}).(types.Drop)
		}).
		ToSlice(&mergedDrops)
	return mergedDrops
}

func AggregateGachaBoxDrops(report *types.SingleReport) {
	report.Times = int(linq.From(report.Drops).
		SelectT(func(drop types.Drop) int {
			return drop.Quantity
		}).
		SumInts())
}
