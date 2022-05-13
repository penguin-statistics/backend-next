package model

import (
	"time"

	"github.com/uptrace/bun"
)

type RejectRule struct {
	bun.BaseModel `bun:"reject_rules"`

	RuleID    int        `bun:",pk,autoincrement" json:"id"`
	CreatedAt *time.Time `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at"`
	Status    string     `bun:"default=active" json:"status"`
	Expr      string     `bun:"expr" json:"expr"`
}
