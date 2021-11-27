package models

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type PZone struct {
	bun.BaseModel `bun:"zones"`

	ID         int64           `json:"id"`
	ZoneID     int64           `json:"zoneId"`
	Index      int64           `json:"index"`
	Category   string          `json:"category"`
	Type       *null.String    `json:"type,omitempty"`
	Name       json.RawMessage `json:"name"`
	Existence  json.RawMessage `json:"existence"`
	Background *null.String    `json:"background,omitempty"`
}
