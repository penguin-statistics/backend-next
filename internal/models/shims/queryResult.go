package shims

import "gopkg.in/guregu/null.v3"

// DropMatrix
type DropMatrixQueryResult struct {
	Matrix []*OneDropMatrixElement `json:"matrix"`
}

type OneDropMatrixElement struct {
	StageID   string    `json:"stageId"`
	ItemID    string    `json:"itemId"`
	Times     int       `json:"times"`
	Quantity  int       `json:"quantity"`
	StartTime int64     `json:"start"`
	EndTime   *null.Int `json:"end,omitempty"`
}
