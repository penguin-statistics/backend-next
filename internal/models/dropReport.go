package models

import (
	"github.com/uptrace/bun"
)

type DropReport struct {
	bun.BaseModel `bun:"drop_reports,alias:dr"`

	ReportID  int    `bun:",pk" json:"id"`
	StageID   int    `json:"stageId"`
	PatternID int    `json:"patternId"`
	Times     int    `json:"times"`
	IP        string `json:"ip"`
	CreatedAt string `json:"createdAt"`
	Deleted   bool   `json:"deleted"`
	Server    string `json:"server"`
	AccountID int    `json:"accountId"`
}