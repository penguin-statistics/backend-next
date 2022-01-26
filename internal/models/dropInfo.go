package models

import (
	"encoding/json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type DropInfo struct {
	bun.BaseModel `bun:"drop_infos,alias:di"`

	DropID      int             `bun:",pk" json:"id"`
	Server      string          `json:"server"`
	StageID     int             `json:"stageId"`
	ItemID      null.Int        `json:"itemId"`
	DropType    string          `json:"dropType"`
	RangeID     int             `json:"timeRangeId"`
	Bounds      json.RawMessage `json:"bounds"`
	Accumulable bool            `json:"accumulable"`
}
