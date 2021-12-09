package models

import (
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type PTimeRange struct {
	bun.BaseModel `bun:"time_ranges,alias:tr"`

	ID        int64        `json:"id"`
	Name      *null.String `json:"name,omitempty"`
	StartTime null.Time    `json:"startTime"`
	EndTime   null.Time    `json:"endTime"`
	Comment   *null.String `json:"-" bun:"-"`
	Server    string       `json:"server"`
}
