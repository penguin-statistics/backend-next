package models

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type Activity struct {
	bun.BaseModel `bun:"activities"`

	ActivityID int             `bun:",pk" json:"id"`
	StartTime  *time.Time      `json:"startTime"`
	EndTime    null.Time       `json:"endTime"`
	Name       string          `json:"name"`
	Existence  json.RawMessage `json:"existence"`
}
