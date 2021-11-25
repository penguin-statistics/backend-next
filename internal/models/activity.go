package models

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type PActivity struct {
	bun.BaseModel `bun:"activities"`

	ID        int64           `json:"id"`
	StartTime *time.Time      `json:"startTime"`
	EndTime   null.Time       `json:"endTime"`
	Name      string          `json:"name"`
	Existence json.RawMessage `json:"existence"`
}
