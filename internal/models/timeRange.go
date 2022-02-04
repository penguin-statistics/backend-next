package models

import (
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type TimeRange struct {
	bun.BaseModel `bun:"time_ranges,alias:tr"`

	RangeID   int          `bun:",pk" json:"id"`
	Name      *null.String `json:"name,omitempty" swaggertype:"string"`
	StartTime *time.Time   `json:"startTime"`
	EndTime   *time.Time   `json:"endTime"`
	Comment   *null.String `json:"-" bun:"-" swaggertype:"string"`
	Server    string       `json:"server"`
}

func (tr *TimeRange) String() string {
	return strconv.FormatInt(tr.StartTime.UnixMilli(), 10) + "-" + strconv.FormatInt(tr.EndTime.UnixMilli(), 10)
}

func (tr *TimeRange) FromString(s string) *TimeRange {
	if s == "" {
		return nil
	}

	var startTime, endTime time.Time
	if strings.Contains(s, "-") {
		times := strings.Split(s, "-")
		
		startTimestamp, err := strconv.ParseInt(times[0], 10, 64)
		if err != nil {
			return nil
		}
		startTime = time.Unix(startTimestamp / 1000, 0)
		endTimestamp, err := strconv.ParseInt(times[1], 10, 64)
		if err != nil {
			return nil
		}
		endTime = time.Unix(endTimestamp / 1000, 0)
	}

	return &TimeRange{
		StartTime: &startTime,
		EndTime:   &endTime,
	}
}
