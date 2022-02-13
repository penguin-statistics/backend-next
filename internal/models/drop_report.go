package models

import (
	"time"

	"github.com/uptrace/bun"
)

type DropReport struct {
	bun.BaseModel `bun:"drop_reports,alias:dr"`

	ReportID    int        `bun:",pk" json:"id"`
	StageID     int        `json:"stageId"`
	PatternID   int        `json:"patternId"`
	Times       int        `json:"times"`
	CreatedAt   *time.Time `json:"createdAt"`
	Reliability int        `json:"reliability"`
	Server      string     `json:"server"`
	AccountID   int        `json:"accountId"`
}
