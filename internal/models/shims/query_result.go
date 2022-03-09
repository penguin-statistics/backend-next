package shims

import "gopkg.in/guregu/null.v3"

// DropMatrix
type DropMatrixQueryResult struct {
	Matrix []*OneDropMatrixElement `json:"matrix"`
}

type OneDropMatrixElement struct {
	StageID   string   `json:"stageId"`
	ItemID    string   `json:"itemId"`
	Times     int      `json:"times"`
	Quantity  int      `json:"quantity"`
	StartTime int64    `json:"start"`
	EndTime   null.Int `json:"end,omitempty" swaggertype:"integer"`
}

// DropPattern
type PatternMatrixQueryResult struct {
	PatternMatrix []*OnePatternMatrixElement `json:"pattern_matrix"`
}

type OnePatternMatrixElement struct {
	StageID   string   `json:"stageId"`
	Pattern   *Pattern `json:"pattern"`
	Times     int      `json:"times"`
	Quantity  int      `json:"quantity"`
	StartTime int64    `json:"start"`
	EndTime   null.Int `json:"end,omitempty" swaggertype:"integer"`
}

type Pattern struct {
	Drops []*OneDrop `json:"drops"`
}

type OneDrop struct {
	ItemID   string `json:"itemId"`
	Quantity int    `json:"quantity"`
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
	AdvancedResults []interface{} `json:"advanced_results"`
}
