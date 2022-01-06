package models

import (
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type TimeRange struct {
	bun.BaseModel `bun:"time_ranges,alias:tr"`

	RangeID   int          `bun:",pk" json:"id"`
	Name      *null.String `json:"name,omitempty" swaggertype:"string"`
	StartTime null.Time    `json:"startTime"`
	EndTime   null.Time    `json:"endTime"`
	Comment   *null.String `json:"-" bun:"-" swaggertype:"string"`
	Server    string       `json:"server"`
}