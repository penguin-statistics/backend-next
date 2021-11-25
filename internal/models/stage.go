package models

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type PStage struct {
	bun.BaseModel `bun:"stages"`

	ID           int64           `json:"id"`
	ArkStageID   string          `json:"ark_stage_id"`
	ZoneID       int64           `json:"zone_id"`
	Code         json.RawMessage `json:"code"`
	Sanity       null.Int        `json:"sanity"`
	Existence    json.RawMessage `json:"existence"`
	MinClearTime null.Int        `json:"min_clear_time"`
}
