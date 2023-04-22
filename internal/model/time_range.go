package model

import (
	"strconv"
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"
)

type TimeRange struct {
	bun.BaseModel `bun:"time_ranges,alias:tr"`

	RangeID   int         `bun:",pk,autoincrement" json:"id"`
	Name      null.String `json:"name,omitempty" swaggertype:"string"`
	StartTime *time.Time  `json:"startTime"`
	EndTime   *time.Time  `json:"endTime"`
	Comment   null.String `json:"comment" swaggertype:"string"`
	Server    string      `json:"server"`
}

func (tr *TimeRange) String() string {
	return strconv.FormatInt(tr.StartTime.UnixMilli(), 10) + "-" + strconv.FormatInt(tr.EndTime.UnixMilli(), 10)
}

func (tr *TimeRange) Includes(t time.Time) bool {
	if tr.StartTime != nil && tr.StartTime.After(t) {
		return false
	}
	if tr.EndTime != nil && tr.EndTime.Before(t) {
		return false
	}
	return true
}

func (tr *TimeRange) HumanReadableString(server string) string {
	return tr.StartTime.In(constant.LocMap[server]).Format("2006-01-02 15:04:05") + " - " + tr.EndTime.In(constant.LocMap[server]).Format("2006-01-02 15:04:05")
}

func TimeRangeFromString(s string) *TimeRange {
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
		startTime = time.Unix(startTimestamp/1000, 0)
		endTimestamp, err := strconv.ParseInt(times[1], 10, 64)
		if err != nil {
			return nil
		}
		endTime = time.Unix(endTimestamp/1000, 0)
	}

	return &TimeRange{
		StartTime: &startTime,
		EndTime:   &endTime,
	}
}
