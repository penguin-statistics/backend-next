package model

import "gopkg.in/guregu/null.v3"

// SiteStatsAggregate is the per stage and item aggregation of drop matrix elements behind the site stats.
// Aggregates of disjoint day ranges add up (see Add).
type SiteStatsAggregate struct {
	StageID int `bun:"stage_id"`
	ItemID  int `bun:"item_id"`
	// Times sums times over the distinct (times, day_num) pairs with times > 0; NULL if there is no such pair.
	Times    null.Int `bun:"times"`
	Quantity int      `bun:"quantity"`
}

// Add accumulates an aggregate of the same stage and item over a disjoint range of days.
func (a *SiteStatsAggregate) Add(other *SiteStatsAggregate) {
	if other.Times.Valid {
		a.Times = null.IntFrom(a.Times.Int64 + other.Times.Int64)
	}
	a.Quantity += other.Quantity
}

// SiteStatsBase holds the site stats aggregates of every day before BeforeDayNum, as of invalidation Generation.
type SiteStatsBase struct {
	BeforeDayNum int
	Generation   int64
	Aggregates   []*SiteStatsAggregate
}

// GlobalDropMatrixTarget is a stage and item of a global drop matrix, with the time range its elements are aggregated
// over (the elements within it).
type GlobalDropMatrixTarget struct {
	StageID   int
	ItemID    int
	TimeRange *TimeRange
}

type GlobalDropMatrixKey struct {
	SourceCategory string
	StageID        int
	ItemID         int
}

// GlobalDropMatrixAggregate aggregates the elements of a target in a source category. Times sums times over the
// distinct (times, day_num) of the elements with times > 0; Quantity and QuantityBuckets sum those with quantity > 0.
// A part no element counts for is invalid (empty for the buckets).
type GlobalDropMatrixAggregate struct {
	Times           null.Int
	Quantity        null.Int
	QuantityBuckets map[int]int
}
