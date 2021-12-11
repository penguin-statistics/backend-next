package models

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type PStage struct {
	bun.BaseModel `bun:"stages"`

	// ID is the numerical ID of the stage.
	ID int64 `json:"penguinStageId"`
	// ArkStageID is the previously used, string form ID of the stage; in JSON-representation `stageId` is used as key.
	ArkStageID string `json:"stageId"`
	// ZoneID is the numerical ID of the zone the stage is in.
	ZoneID int64 `json:"zoneId"`
	// Code is a map with language code as key and the code of the stage in that language as value.
	Code json.RawMessage `json:"code"`
	// Sanity is the sanity requirement for a full clear of the stage.
	Sanity null.Int `json:"sanity"`
	// Existence is a map with server code as key and the existence of the item in that server as value.
	Existence json.RawMessage `json:"existence"`
	// MinClearTime is the minimum time (in milliseconds as a duration) it takes to clear the stage, referencing from prts.wiki
	MinClearTime null.Int `json:"minClearTime"`
}
