package shims

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type PStage struct {
	bun.BaseModel `bun:"stages"`

	ID           int64           `json:"penguinStageId"`
	ArkStageID   string          `json:"stageId"`
	ZoneID       int64           `json:"zoneId"`
	Code         json.RawMessage `json:"code"`
	Sanity       null.Int        `json:"sanity"`
	Existence    json.RawMessage `json:"existence"`
	MinClearTime null.Int        `json:"minClearTime"`
}
