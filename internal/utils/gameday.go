package utils

import (
	"time"

	"github.com/penguin-statistics/backend-next/internal/constants"
)

var locMap = map[string]*time.Location{
	"CN": time.FixedZone("UTC+8", +8*60*60),
	"US": time.FixedZone("UTC-7", -7*60*60),
	"JP": time.FixedZone("UTC+9", +9*60*60),
	"KR": time.FixedZone("UTC+9", +9*60*60),
}

func GetGameDayStartTime(server string, t time.Time) time.Time {
	loc := locMap[server]
	t = t.In(loc)
	if t.Hour() < 4 {
		t = t.AddDate(0, 0, -1)
	}
	newT := time.Date(t.Year(), t.Month(), t.Day(), 4, 0, 0, 0, loc)
	return newT
}
func GetGameDayEndTime(server string, t time.Time) time.Time {
	return GetGameDayStartTime(server, t).AddDate(0, 0, 1)
}

func IsGameDayStartTime(server string, t time.Time) bool {
	loc := locMap[server]
	t = t.In(loc)
	return t.Hour() == constants.GameDayStartHour &&
		t.Minute() == constants.GameDayStartMinute &&
		t.Second() == constants.GameDayStartSecond &&
		t.Nanosecond() == constants.GameDayStartNano
}
