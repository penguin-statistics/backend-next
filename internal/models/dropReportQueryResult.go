package models

import "time"

// DropMatrix
type TotalQuantityResultForDropMatrix struct {
	StageID int `json:"stageId" bun:"stage_id"`
	ItemID int `json:"itemId" bun:"item_id"`
	TotalQuantity int `json:"totalQuantity" bun:"total_quantity"`
}

type TotalTimesResult struct {
	StageID int `json:"stageId" bun:"stage_id"`
	TotalTimes int `json:"totalTimes" bun:"total_times"`
}

type CombinedResultForDropMatrix struct {
	StageID int `json:"stageId"`
	ItemID int `json:"itemId"`
	Times int `json:"times"`
	Quantity int `json:"quantity"`
	TimeRange *TimeRange `json:"timeRange"`
}

type DropMatrixQueryResult struct {
	Matrix []*OneDropMatrixElement `json:"matrix"`
}

type OneDropMatrixElement struct {
	StageID int `json:"stageId"`
	ItemID int `json:"itemId"`
	Times int `json:"times"`
	Quantity int `json:"quantity"`
	TimeRange *TimeRange `json:"timeRange"`
}

// DropPattern
type TotalQuantityResultForPatternMatrix struct {
	StageID int `json:"stageId" bun:"stage_id"`
	PatternID int `json:"patternId" bun:"pattern_id"`
	TotalQuantity int `json:"totalQuantity" bun:"total_quantity"`
}

type CombinedResultForDropPattern struct {
	StageID int `json:"stageId"`
	PatternID int `json:"patternId"`
	Times int `json:"times"`
	Quantity int `json:"quantity"`
}

type DropPatternQueryResult struct {
	DropPatterns []*OneDropPattern `json:"dropPatterns"`
}

type OneDropPattern struct {
	StageID int `json:"stageId"`
	PatternID int `json:"patternId"`
	TimeRange *TimeRange `json:"timeRange"`
	Times int `json:"times"`
	Quantity int `json:"quantity"`	
}

// Trend
type TotalQuantityResultForTrend struct {
	GroupID int `json:"groupId" bun:"group_id"`
	IntervalStart *time.Time `json:"intervalStart" bun:"interval_start"`
	IntervalEnd *time.Time `json:"intervalEnd" bun:"interval_end"`
	StageID int `json:"stageId" bun:"stage_id"`
	ItemID int `json:"itemId" bun:"item_id"`
	TotalQuantity int `json:"totalQuantity" bun:"total_quantity"`
}

type TotalTimesResultForTrend struct {
	GroupID int `json:"groupId" bun:"group_id"`
	IntervalStart *time.Time `json:"intervalStart" bun:"interval_start"`
	IntervalEnd *time.Time `json:"intervalEnd" bun:"interval_end"`
	StageID int `json:"stageId" bun:"stage_id"`
	TotalTimes int `json:"totalTimes" bun:"total_times"`
}

type CombinedResultForTrend struct {
	GroupID int `json:"groupId"`
	StartTime *time.Time `json:"startTime"`
	EndTime *time.Time `json:"endTime"`
	StageID int `json:"stageId"`
	ItemID int `json:"itemId"`
	Times int `json:"times"`
	Quantity int `json:"quantity"`
}

type TrendQueryResult struct {
	Trends []*StageTrend `json:"trends"`
}

type StageTrend struct {
	StageID int `json:"stageId"`
	Results []*ItemTrend `json:"results"`
}

type ItemTrend struct {
	ItemID int `json:"itemId"`
	StartTime *time.Time `json:"startTime"`
	Times []int `json:"times"`
	Quantity []int `json:"quantity"`
}
