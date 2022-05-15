package model

import (
	"time"

	"github.com/uptrace/bun"
)

type TrendElement struct {
	bun.BaseModel `bun:"trend_elements,alias:te"`

	ElementID      int        `bun:",pk,autoincrement" json:"id"`
	StageID        int        `json:"stageId"`
	ItemID         int        `json:"itemId"`
	GroupID        int        `json:"groupId"`
	StartTime      *time.Time `json:"startTime"`
	EndTime        *time.Time `json:"endTime"`
	Quantity       int        `json:"quantity"`
	Times          int        `json:"times"`
	Server         string     `json:"server"`
	SourceCategory string     `json:"sourceCategory"` // sourceCategory can be: "automated", "manual", "all"
}
