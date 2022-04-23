package v2

import "gopkg.in/guregu/null.v3"

// DropMatrix
type DropMatrixQueryResult struct {
	Matrix []*OneDropMatrixElement `json:"matrix"`
}

type OneDropMatrixElement struct {
	StageID   string   `json:"stageId" example:"main_01-07"`
	ItemID    string   `json:"itemId" example:"30012"`
	Times     int      `json:"times" example:"1061347"`
	Quantity  int      `json:"quantity" example:"1322056"`
	StartTime int64    `json:"start" example:"1556676000000"`
	EndTime   null.Int `json:"end,omitempty" swaggertype:"integer"`
}

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
	Drops []*OneDrop `json:"drops"`
}

type OneDrop struct {
	ItemID   string `json:"itemId" example:"30012"`
	Quantity int    `json:"quantity" example:"1"`
}

// Trend
type TrendQueryResult struct {
	Trend map[string]*StageTrend `json:"trend"`
}

type StageTrend struct {
	Results   map[string]*OneItemTrend `json:"results"`
	StartTime int64                    `json:"startTime"`
}

type OneItemTrend struct {
	Quantity []int `json:"quantity"`
	Times    []int `json:"times"`
}

// Advanced Query
type AdvancedQueryResult struct {
	AdvancedResults []any `json:"advanced_results"`
}
