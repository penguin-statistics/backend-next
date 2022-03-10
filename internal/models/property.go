package models

import (
	"github.com/uptrace/bun"
)

type Property struct {
	bun.BaseModel `bun:"properties"`

	PropertyID int    `bun:",pk,autoincrement" json:"id"`
	Key        string `json:"key"`
	Value      string `json:"value"`
}
