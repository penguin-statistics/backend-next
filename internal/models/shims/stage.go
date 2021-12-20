package shims

import (
	"encoding/json"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type Stage struct {
	bun.BaseModel `bun:"stages"`

	StageID      int64           `bun:",pk" json:"-"`
	ArkStageID   string          `json:"stageId"`
	ZoneID       int64           `json:"-"`
	ArkZoneID    string          `bun:"-" json:"zoneId"`
	Code         string          `bun:"-" json:"code"`
	CodeI18n     json.RawMessage `bun:"code" json:"code_i18n"`
	Sanity       null.Int        `json:"apCost" swaggertype:"number"`
	Existence    json.RawMessage `json:"existence"`
	MinClearTime null.Int        `json:"minClearTime" swaggertype:"number"`

	Zone *models.Zone `bun:"rel:belongs-to,join:zone_id=zone_id" json:"-"`

	DropInfos []*DropInfo `bun:"rel:has-many,join:stage_id=stage_id" json:"dropInfos"`
}
