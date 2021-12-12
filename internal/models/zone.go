package models

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type Zone struct {
	bun.BaseModel `bun:"zones"`

	// ZoneID is the numerical ID of the zone.
	ZoneID    int64  `bun:",pk" json:"penguinZoneId"`
	ArkZoneID string `json:"zoneId"`
	Index     int64  `json:"index"`
	// Category of the zone.
	Category string `json:"category" example:"MAINLINE"`
	// Type of the zone, e.g. "AWAKENING_HOUR" or "VISION_SHATTER". Optional and only occurres when `category` is "MAINLINE".
	Type *null.String `json:"type,omitempty" swaggertype:"string" example:"AWAKENING_HOUR"`
	// Name is a map with language code as key and the name of the item in that language as value.
	Name json.RawMessage `json:"name"`
	// Existence is a map with server code as key and the existence of the item in that server as value.
	Existence json.RawMessage `json:"existence"`
	// Background is the path of the background image of the zone, relative to the CDN endpoint.
	Background *null.String `json:"background,omitempty" swaggertype:"string"`
}
