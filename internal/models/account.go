package models

import (
	"time"

	"github.com/uptrace/bun"
)

type Account struct {
	bun.BaseModel `bun:"accounts"`

	AccountID int     `bun:",pk" json:"id"`
	PenguinID string  `json:"penguinId"`
	Weight    float64 `json:"weight"`
	// Tags      []string `json:"tags"`
	CreatedAt time.Time `json:"createdAt"`
}
