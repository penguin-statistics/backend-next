package model

import (
	"github.com/goccy/go-json"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

// DropInfo describes the drops of a stage during a time range: of one item, or, without an item, of the stage.
//
// Accumulable tells whether the drops of the item in this time range are carried into the later time ranges of the
// stage and item. Consecutive accumulable time ranges are counted together; a time range that is not accumulable is
// counted on its own, without the time ranges before or after it. With A false, B and C true (in chronological
// order) the latest window is B~C; when the latest time range is not accumulable, it is the latest window by itself.
// The global and the personal drop matrices use the same rule (see #794 and service.accumulableSegments).
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
