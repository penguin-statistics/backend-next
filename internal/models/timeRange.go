package models

import (
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type PTimeRange struct {
	bun.BaseModel `bun:"time_ranges"`

	ID        int64       `json:"id"`
	Name      null.String `json:"name"`
	StartTime null.Time   `json:"start_time"`
	EndTime   null.Time   `json:"end_time"`
	Comment   null.String `json:"comment"`
	Server    string      `json:"server"`
}
