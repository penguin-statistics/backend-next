package v3

import "gopkg.in/guregu/null.v3"

// DropPattern
type PatternMatrixQueryResult struct {
	PatternMatrix []*OnePatternMatrixElement `json:"pattern_matrix"`
}

type OnePatternMatrixElement struct {
	StageID   string   `json:"stageId" example:"main_01-07"`
	Pattern   *Pattern `json:"pattern"`
	Times     int      `json:"times" example:"641734"`
	Quantity  int      `json:"quantity" example:"159486"`
	StartTime int64    `json:"start" example:"1633032000000"`
	EndTime   null.Int `json:"end,omitempty" swaggertype:"integer" extensions:"x-nullable"`
}

type Pattern struct {
	PatternID int        `json:"patternId" example:"1"`
	Drops     []*OneDrop `json:"drops"`
}

type OneDrop struct {
	ItemID   string `json:"itemId" example:"30012"`
	Quantity int    `json:"quantity" example:"1"`
}
