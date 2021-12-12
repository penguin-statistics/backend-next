package models

import (
	"github.com/uptrace/bun"
)

type Property struct {
	bun.BaseModel `bun:"properties"`

	PropertyID int64  `bun:",pk" json:"id"`
	Key        string `json:"key"`
	Value      string `json:"value"`
}
