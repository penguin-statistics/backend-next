package models

import (
	"github.com/uptrace/bun"
)

type DropReport struct {
	bun.BaseModel `bun:"drop_reports"`

	ReportID  int64  `bun:",pk" json:"id"`
	StageID   int64  `json:"stageId"`
	PatternID int64  `json:"patternId"`
	Times     int64  `json:"times"`
	IP        string `json:"ip"`
	CreatedAt string `json:"createdAt"`
	Deleted   bool   `json:"deleted"`
	Server    string `json:"server"`
	AccountID int64  `json:"accountId"`
}
