package model

// GlobalPatternMatrixTarget is a stage of a global pattern matrix, with the time range its elements are aggregated
// over (the elements within it).
type GlobalPatternMatrixTarget struct {
	StageID   int
	TimeRange *TimeRange
}

// GlobalPatternMatrixAggregates aggregates the elements of the targets of a source category: per stage, the times
// summed over days of the smallest times > 0 of a day, and per stage and pattern, the quantity > 0 summed.
type GlobalPatternMatrixAggregates struct {
	TimesByStageID      map[int]int
	QuantitiesByStageID map[int]map[int]int
}
