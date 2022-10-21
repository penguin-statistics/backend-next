package v2

import (
	"github.com/goccy/go-json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
)

type Stage struct {
	bun.BaseModel `bun:"stages"`

	StageID    int    `bun:",pk,autoincrement" json:"-"`
	ArkStageID string `json:"stageId" example:"main_01-07"`
	ZoneID     int    `json:"-"`
	ArkZoneID  string `bun:"-" json:"zoneId" example:"main_1"`
	// StageType is the type of the stage
	// * MAIN - Mainline Stages
	// * SUB - Sub Stages
	// * ACTIVITY - Activity Stages
	// * DAILY - Daily Stages
	StageType       string          `json:"stageType" enums:"MAIN,SUB,ACTIVITY,DAILY" example:"MAIN"`
	Code            string          `bun:"-" json:"code" example:"1-7"`
	CodeI18n        json.RawMessage `bun:"code" json:"code_i18n" swaggertype:"object"`
	Sanity          null.Int        `json:"apCost" swaggertype:"integer" example:"6"`
	Existence       json.RawMessage `json:"existence" swaggertype:"object"`
	MinClearTime    null.Int        `json:"minClearTime" swaggertype:"integer" extension:"x-nullable" example:"118000"`
	RecognitionOnly []string        `bun:"-" json:"recognitionOnly,omitempty" extension:"x-nullable"`

	Zone *model.Zone `bun:"rel:belongs-to,join:zone_id=zone_id" json:"-"`

	DropInfos []*DropInfo `bun:"rel:has-many,join:stage_id=stage_id" json:"dropInfos,omitempty"`
}
