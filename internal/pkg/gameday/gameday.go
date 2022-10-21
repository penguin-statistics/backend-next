package gameday

import (
	"time"

	"exusiai.dev/gommon/constant"
)

func StartTime(server string, t time.Time) time.Time {
	loc := constant.LocMap[server]
	t = t.In(loc)
	if t.Hour() < 4 {
		t = t.Add(time.Hour * -24)
	}
	newT := time.Date(t.Year(), t.Month(), t.Day(), constant.GameDayStartHour, constant.GameDayStartMinute, constant.GameDayStartSecond, constant.GameDayStartNano, loc)
	return newT
}

func EndTime(server string, t time.Time) time.Time {
	return StartTime(server, t).Add(time.Hour * 24)
}

func IsStartTime(server string, t time.Time) bool {
	loc := constant.LocMap[server]
	t = t.In(loc)
	return t.Hour() == constant.GameDayStartHour &&
		t.Minute() == constant.GameDayStartMinute &&
		t.Second() == constant.GameDayStartSecond &&
		t.Nanosecond() == constant.GameDayStartNano
}
