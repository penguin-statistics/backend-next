package models

import (
	"github.com/uptrace/bun"
)

type PDropPattern struct {
	bun.BaseModel `bun:"drop_patterns"`

	ID   int64  `json:"id"`
	Hash string `json:"hash"`
}
