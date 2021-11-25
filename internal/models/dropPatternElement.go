package models

import (
	"github.com/uptrace/bun"
)

type PDropPatternElement struct {
	bun.BaseModel `bun:"drop_pattern_elements"`

	ID            int64 `json:"id"`
	DropPatternID int64 `json:"dropPatternId"`
	ItemID        int64 `json:"itemId"`
	Quantity      int64 `json:"quantity"`
}
