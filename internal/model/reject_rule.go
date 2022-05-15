package model

import (
	"time"

	"github.com/uptrace/bun"
)

type RejectRule struct {
	bun.BaseModel `bun:"reject_rules"`

	RuleID          int        `bun:",pk,autoincrement" json:"id"`
	CreatedAt       *time.Time `bun:"created_at" json:"created_at"`
	UpdatedAt       *time.Time `bun:"updated_at" json:"updated_at"`
	Status          int        `bun:"status" json:"status"`
	Expr            string     `bun:"expr" json:"expr"`
	WithReliability int        `bun:"with_reliability" json:"with_reliability"`
}
