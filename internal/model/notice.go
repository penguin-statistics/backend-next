package model

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type Notice struct {
	bun.BaseModel `bun:"notices"`

	NoticeID  int             `bun:",pk,autoincrement" json:"id"`
	Existence json.RawMessage `json:"existence" swaggertype:"object"`
	Severity  null.Int        `json:"severity" swaggertype:"integer"`
	Content   json.RawMessage `json:"content_i18n"`
}
