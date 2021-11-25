package models

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type PDropInfo struct {
	bun.BaseModel `bun:"drop_infos"`

	ID            int64           `json:"id"`
	Server        string          `json:"server"`
	StageID       int64           `json:"stageId"`
	ItemID        null.Int        `json:"itemId"`
	DropType      int64           `json:"dropType"`
	TimeRangeID   string          `json:"timeRangeId"`
	Bounds        json.RawMessage `json:"bounds"`
	Accumulatable bool            `json:"accumulatable"`
}
