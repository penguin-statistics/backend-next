package model

import (
	"time"
)

type TrendElement struct {
	ElementID      int        `json:"id"`
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
