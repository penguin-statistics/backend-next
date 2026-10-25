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

// Add accumulates the aggregates of the same targets and source category over a disjoint range of days: the times of
// a stage present in either are present, with the sum of both, and the quantities add per stage and pattern. The
// elements of a day are on one side only, and so is the smallest times of a day. The maps of a are updated in place.
func (a *GlobalPatternMatrixAggregates) Add(other *GlobalPatternMatrixAggregates) {
	if len(other.TimesByStageID) > 0 && a.TimesByStageID == nil {
		a.TimesByStageID = make(map[int]int, len(other.TimesByStageID))
	}
	for stageId, times := range other.TimesByStageID {
		a.TimesByStageID[stageId] += times
	}
	if len(other.QuantitiesByStageID) > 0 && a.QuantitiesByStageID == nil {
		a.QuantitiesByStageID = make(map[int]map[int]int, len(other.QuantitiesByStageID))
	}
	for stageId, otherQuantities := range other.QuantitiesByStageID {
		quantities, ok := a.QuantitiesByStageID[stageId]
		if !ok {
			quantities = make(map[int]int, len(otherQuantities))
			a.QuantitiesByStageID[stageId] = quantities
		}
		for patternId, quantity := range otherQuantities {
			quantities[patternId] += quantity
		}
	}
}
