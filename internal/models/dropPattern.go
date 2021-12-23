package models

import (
	"github.com/uptrace/bun"
)

type DropPattern struct {
	bun.BaseModel `bun:"drop_patterns,alias:dp"`

	PatternID int64  `bun:",pk" json:"id"`
	Hash      string `json:"hash"`
}
