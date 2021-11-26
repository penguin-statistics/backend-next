package models

import (
	"github.com/uptrace/bun"
)

type PAccount struct {
	bun.BaseModel `bun:"accounts"`

	ID        int64     `json:"id"`
	PenguinID string   `json:"penguinId"`
	Weight    float64  `json:"weight"`
	Tags      []string `json:"tags"`
	CreatedAt string   `json:"createdAt"`
}