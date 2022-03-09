package models

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type Stage struct {
	bun.BaseModel `bun:"stages,alias:st"`

	// StageID (penguinStageId) is the numerical ID of the stage.
	StageID int `bun:",pk" json:"penguinStageId"`
	// ArkStageID (stageId) is the previously used, string form ID of the stage; in JSON-representation `stageId` is used as key.
	ArkStageID string `json:"stageId"`
	// ZoneID is the numerical ID of the zone the stage is in.
	ZoneID int `json:"zoneId"`
	// StageType is the type of the stage, e.g. "MAIN", "SUB", "ACTIVITY" and "DAILY".
	StageType string `json:"stageType"`
	// ExtraProcessType is the type of extra process that is used in the stage, e.g. "GACHABOX".
	ExtraProcessType null.String `json:"extraProcessType" swaggertype:"string"`
	// Code is a map with language code as key and the code of the stage in that language as value.
	Code json.RawMessage `json:"code"`
	// Sanity is the sanity requirement for a full clear of the stage.
	Sanity null.Int `json:"sanity" swaggertype:"integer"`
	// Existence is a map with server code as key and the existence of the item in that server as value.
	Existence json.RawMessage `json:"existence" swaggertype:"object"`
	// MinClearTime is the minimum time (in milliseconds as a duration) it takes to clear the stage, referencing from prts.wiki
	MinClearTime null.Int `json:"minClearTime" swaggertype:"integer"`
}

type StageExtended struct {
	Stage

	Zone *Zone `bun:"rel:belongs-to,join:zone_id=zone_id" json:"-"`
}
