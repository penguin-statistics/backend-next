package models

import (
	"github.com/uptrace/bun"
)

type PatternMatrixElement struct {
	bun.BaseModel `bun:"pattern_matrix_elements,alias:pme"`

	ElementID int    `bun:",pk,autoincrement" json:"id"`
	StageID   int    `json:"stageId"`
	PatternID int    `json:"patternId"`
	RangeID   int    `json:"rangeId"`
	Quantity  int    `json:"quantity"`
	Times     int    `json:"times"`
	Server    string `json:"server"`
}
