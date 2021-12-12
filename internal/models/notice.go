package models

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type Notice struct {
	bun.BaseModel `bun:"notices"`

	NoticeID   int64           `bun:",pk" json:"id"`
	Conditions json.RawMessage `json:"conditions"`
	Severity   null.Int        `json:"severity"`
	Content    string          `json:"content"`
}
