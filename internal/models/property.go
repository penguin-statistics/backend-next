package models

import (
	"github.com/uptrace/bun"
)

type PProperty struct {
	bun.BaseModel `bun:"properties"`

	ID    int64  `json:"id"`
	Key   string `json:"key"`
	Value string `json:"value"`
}
