package model

import (
	"time"

	"github.com/goccy/go-json"

	"github.com/uptrace/bun"
)

type Activity struct {
	bun.BaseModel `bun:"activities"`

	ActivityID int             `bun:",pk,autoincrement" json:"id"`
	StartTime  *time.Time      `json:"startTime"`
	EndTime    *time.Time      `json:"endTime"`
	Name       json.RawMessage `json:"name"`
	Existence  json.RawMessage `json:"existence"`
}
