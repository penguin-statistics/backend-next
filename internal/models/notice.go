package models

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type Notice struct {
	bun.BaseModel `bun:"notices"`

	NoticeID  int             `bun:",pk" json:"id"`
	Existence json.RawMessage `json:"existence"`
	Severity  null.Int        `json:"severity"`
	Content   json.RawMessage `json:"content_i18n"`
}
