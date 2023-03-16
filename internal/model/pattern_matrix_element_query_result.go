package model

type AllTimesResultForGlobalPatternMatrix struct {
	StageID int `json:"stageId" bun:"stage_id"`
	Times   int `json:"times" bun:"times"`
}

type AllQuantitiesResultForGlobalPatternMatrix struct {
	StageID   int `json:"stageId" bun:"stage_id"`
	PatternID int `json:"patternId" bun:"pattern_id"`
	Quantity  int `json:"quantity" bun:"quantity"`
}
