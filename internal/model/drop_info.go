package model

import (
	"github.com/goccy/go-json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type DropInfo struct {
	bun.BaseModel `bun:"drop_infos,alias:di"`

	DropID      int             `bun:",pk,autoincrement" json:"id"`
	Server      string          `json:"server"`
	StageID     int             `json:"stageId"`
	ItemID      null.Int        `json:"itemId" swaggertype:"integer"`
	DropType    string          `json:"dropType"`
	RangeID     int             `json:"rangeId"`
	Accumulable bool            `json:"accumulable"`
	Bounds      *Bounds         `json:"bounds"`
	Extras      json.RawMessage `json:"extras,omitempty"`
}

type Bounds struct {
	Upper      int   `json:"upper"`
	Lower      int   `json:"lower"`
	Exceptions []int `json:"exceptions,omitempty"`
}

func (b *Bounds) Scan(src any) error {
	return json.Unmarshal(src.([]byte), b)
}
