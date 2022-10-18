package v3

import (
	"github.com/goccy/go-json"

	"gopkg.in/guregu/null.v3"
)

type Zone struct {
	ZoneID     int             `bun:",pk,autoincrement" json:"pgZoneId"`
	ArkZoneID  string          `json:"arkZoneId"`
	Index      int             `json:"index"`
	Category   string          `json:"category" example:"MAINLINE"`
	Type       null.String     `json:"type,omitempty" swaggertype:"string" example:"AWAKENING_HOUR"`
	Name       json.RawMessage `json:"name"`
	Existence  json.RawMessage `json:"existence" swaggertype:"object"`
	Background null.String     `json:"background,omitempty" swaggertype:"string"`
}
