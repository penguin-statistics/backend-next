package models

import (
	"github.com/uptrace/bun"
)

type DropPatternElement struct {
	bun.BaseModel `bun:"drop_pattern_elements,alias:dpe"`

	ElementID     int `bun:",pk,autoincrement" json:"id"`
	DropPatternID int `json:"dropPatternId"`
	ItemID        int `json:"itemId"`
	Quantity      int `json:"quantity"`
}
