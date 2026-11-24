package model

import (
	"time"

	"gopkg.in/guregu/null.v3"
)

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

// Add accumulates an aggregate of the same target and source category over a disjoint range of days: a part valid
// in either is valid, with the sum of both, and the buckets add per key. The elements of a day are on one side only,
// and so is every distinct (times, day_num), so the sum is the aggregate of both ranges of days. The buckets of a are updated in place.
func (a *GlobalDropMatrixAggregate) Add(other *GlobalDropMatrixAggregate) {
	if other.Times.Valid {
		a.Times = null.IntFrom(a.Times.ValueOrZero() + other.Times.Int64)
	}
	if other.Quantity.Valid {
		a.Quantity = null.IntFrom(a.Quantity.ValueOrZero() + other.Quantity.Int64)
	}
	if len(other.QuantityBuckets) > 0 && a.QuantityBuckets == nil {
		a.QuantityBuckets = make(map[int]int, len(other.QuantityBuckets))
	}
	for quantity, count := range other.QuantityBuckets {
		a.QuantityBuckets[quantity] += count
	}
}

// GlobalMatrixScope selects the days of the elements a partial aggregation of the global matrices reads, and splits
// them in two.
type GlobalMatrixScope struct {
	// SplitDayNum splits the elements: those with day_num < SplitDayNum are aggregated apart from the others.
	SplitDayNum int
	// FromDayNum, if valid, leaves out the elements with day_num < FromDayNum.
	FromDayNum null.Int
	// StageFirst reads the elements of the stages of the targets before joining them with the targets: faster for the
	// targets of a few stages over every day, slower for those of every stage.
	StageFirst bool
}

// Clone returns a copy of a that shares nothing with it.
func (a *GlobalDropMatrixAggregate) Clone() *GlobalDropMatrixAggregate {
	clone := &GlobalDropMatrixAggregate{Times: a.Times, Quantity: a.Quantity}
	if a.QuantityBuckets != nil {
		clone.QuantityBuckets = make(map[int]int, len(a.QuantityBuckets))
		for quantity, count := range a.QuantityBuckets {
			clone.QuantityBuckets[quantity] = count
		}
	}
	return clone
}

// GlobalMatrixStageItem is a stage and item of a global drop matrix, in a source category known from the context.
type GlobalMatrixStageItem struct {
	StageID int
	ItemID  int
}

// GlobalMatrixSecondRange is the time range of a target of a global matrix, as the aggregation compares it: in
// whole seconds since the epoch.
type GlobalMatrixSecondRange struct {
	Start int64
	End   int64
}

// GlobalDropMatrixBase is the aggregate of the drop matrix elements of the days before BeforeDayNum, of a server and
// source category, for the targets in Targets: read in one snapshot, in which the fingerprint of those days (the
// identity of the triggers that version them and their day versions) was Fingerprint. It is immutable once cached:
// a refresh makes a new one, which may share the aggregates it did not change.
type GlobalDropMatrixBase struct {
	BeforeDayNum int
	Fingerprint  string
	// Targets holds every target the base was aggregated for, also those without an element, with its time range.
	Targets map[GlobalMatrixStageItem]GlobalMatrixSecondRange
	// Partials holds the aggregates of the targets that have elements in those days.
	Partials map[GlobalMatrixStageItem]*GlobalDropMatrixAggregate
	// BuiltAt is when the base was last aggregated from every day; extending it keeps it.
	BuiltAt time.Time
}
