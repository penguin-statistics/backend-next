package models

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type PNotice struct {
	bun.BaseModel `bun:"notices"`

	ID         int64           `json:"id"`
	Conditions json.RawMessage `json:"conditions"`
	Severity   null.Int        `json:"severity"`
	Content    string          `json:"content"`
}
