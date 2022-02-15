package models

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

type Activity struct {
	bun.BaseModel `bun:"activities"`

	ActivityID int             `bun:",pk" json:"id"`
	StartTime  *time.Time      `json:"startTime"`
	EndTime    *time.Time      `json:"endTime"`
	Name       json.RawMessage `json:"name"`
	Existence  json.RawMessage `json:"existence"`
}
