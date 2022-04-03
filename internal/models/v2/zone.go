package v2

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/models"
)

type Zone struct {
	bun.BaseModel `bun:"zones"`

	ZoneID       int             `bun:",pk,autoincrement" json:"-"`
	ArkZoneID    string          `json:"zoneId"`
	Index        int             `json:"zoneIndex"`
	Category     string          `json:"type"`
	Type         null.String     `json:"subType" swaggertype:"string"`
	ZoneName     string          `bun:"-" json:"zoneName"`
	ZoneNameI18n json.RawMessage `bun:"name" json:"zoneName_i18n" swaggertype:"object"`
	Existence    json.RawMessage `json:"existence" swaggertype:"object"`
	Background   null.String     `json:"background,omitempty" swaggertype:"string"`
	StageIds     []string        `bun:"-" json:"stages"`

	Stages []*models.Stage `bun:"rel:has-many,join:zone_id=zone_id" json:"-"`
}
