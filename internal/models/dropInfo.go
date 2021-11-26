package models

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type PDropInfo struct {
	bun.BaseModel `bun:"drop_infos,alias:di"`

	ID            int64           `json:"id"`
	Server        string          `json:"server"`
	StageID       int64           `json:"stageId"`
	ItemID        null.Int        `json:"itemId"`
	DropType      string          `json:"dropType"`
	TimeRangeID   int64           `json:"timeRangeId"`
	Bounds        json.RawMessage `json:"bounds"`
	Accumulable   bool            `json:"accumulable"`
}
