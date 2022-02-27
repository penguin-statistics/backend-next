package shims

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type Activity struct {
	bun.BaseModel `bun:"activities"`

	ActivityID int             `bun:",pk" json:"-"`
	Start      int64           `json:"start"`
	End        *null.Int       `json:"end,omitempty"`
	LabelI18n  json.RawMessage `json:"label_i18n"`
	Existence  json.RawMessage `json:"existence"`
}
