package models

import (
	"github.com/uptrace/bun"
)

type DropPatternElement struct {
	bun.BaseModel `bun:"drop_pattern_elements"`

	ElementID     int64 `bun:",pk" json:"id"`
	DropPatternID int64 `json:"dropPatternId"`
	ItemID        int64 `json:"itemId"`
	Quantity      int64 `json:"quantity"`
}
