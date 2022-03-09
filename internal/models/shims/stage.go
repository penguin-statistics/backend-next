package shims

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/models"
)

type Stage struct {
	bun.BaseModel `bun:"stages"`

	StageID         int             `bun:",pk" json:"-"`
	ArkStageID      string          `json:"stageId"`
	ZoneID          int             `json:"-"`
	ArkZoneID       string          `bun:"-" json:"zoneId"`
	StageType       string          `json:"stageType"`
	Code            string          `bun:"-" json:"code"`
	CodeI18n        json.RawMessage `bun:"code" json:"code_i18n" swaggertype:"object"`
	Sanity          null.Int        `json:"apCost" swaggertype:"integer"`
	Existence       json.RawMessage `json:"existence" swaggertype:"object"`
	MinClearTime    null.Int        `json:"minClearTime" swaggertype:"integer"`
	RecognitionOnly []string        `bun:"-" json:"recognitionOnly,omitempty"`

	Zone *models.Zone `bun:"rel:belongs-to,join:zone_id=zone_id" json:"-"`

	DropInfos []*DropInfo `bun:"rel:has-many,join:stage_id=stage_id" json:"dropInfos,omitempty"`
}
