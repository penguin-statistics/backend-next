package models

import (
	"github.com/uptrace/bun"
)

type PDropReport struct {
	bun.BaseModel `bun:"drop_reports"`

	ID        int64  `json:"id"`
	StageID   int64  `json:"stageId"`
	PatternID int64  `json:"patternId"`
	Times     int64  `json:"times"`
	IP        string `json:"ip"`
	CreatedAt string `json:"createdAt"`
	Deleted   bool   `json:"deleted"`
	Server    string `json:"server"`
	AccountID int64  `json:"accountId"`
}
