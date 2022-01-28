package models

import (
	"github.com/uptrace/bun"
)

type DropMatrixElement struct {
	bun.BaseModel `bun:"drop_matrix_elements,alias:dme"`

	ElementID int    `bun:",pk" json:"id"`
	StageID   int    `json:"stageId"`
	ItemID    int    `json:"itemId"`
	RangeID   int    `json:"rangeId"`
	Quantity  int    `json:"quantity"`
	Times     int    `json:"times"`
	Server    string `json:"server"`
}
