package model

import (
	"time"

	"github.com/uptrace/bun"
)

type PatternMatrixElement struct {
	bun.BaseModel `bun:"pattern_matrix_elements,alias:pme"`

	ElementID      int        `bun:",pk,autoincrement" json:"id"`
	StageID        int        `json:"stageId"`
	PatternID      int        `json:"patternId"`
	StartTime      *time.Time `json:"startTime"`
	EndTime        *time.Time `json:"endTime"`
	DayNum         int        `json:"dayNum"`
	Quantity       int        `json:"quantity"`
	Times          int        `json:"times"`
	Server         string     `json:"server"`
	SourceCategory string     `json:"sourceCategory"` // sourceCategory can be: "automated", "manual", "all"

	RangeID int `bun:"-" json:"-"`
}
