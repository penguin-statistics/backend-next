package model

import "gopkg.in/guregu/null.v3"

type AllTimesResultForGlobalDropMatrix struct {
	StageID int `json:"stageId" bun:"stage_id"`
	ItemID  int `json:"itemId" bun:"item_id"`
	Times   int `json:"times" bun:"times"`
}

type AllQuantitiesResultForGlobalDropMatrix struct {
	StageID  int `json:"stageId" bun:"stage_id"`
	ItemID   int `json:"itemId" bun:"item_id"`
	Quantity int `json:"quantity" bun:"quantity"`
}

type AllQuantityBucketsResultForGlobalDropMatrix struct {
	StageID         int         `json:"stageId" bun:"stage_id"`
	ItemID          int         `json:"itemId" bun:"item_id"`
	QuantityBuckets map[int]int `json:"quantityBuckets" bun:"type:jsonb"`
}

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
