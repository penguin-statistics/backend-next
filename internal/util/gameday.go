package util

import (
	"time"

	"github.com/penguin-statistics/backend-next/internal/constant"
)

func GetGameDayStartTime(server string, t time.Time) time.Time {
	loc := constant.LocMap[server]
	t = t.In(loc)
	if t.Hour() < 4 {
		t = t.Add(time.Hour * -24)
	}
	newT := time.Date(t.Year(), t.Month(), t.Day(), constant.GameDayStartHour, constant.GameDayStartMinute, constant.GameDayStartSecond, constant.GameDayStartNano, loc)
	return newT
}

func GetGameDayEndTime(server string, t time.Time) time.Time {
	return GetGameDayStartTime(server, t).Add(time.Hour * 24)
}

func IsGameDayStartTime(server string, t time.Time) bool {
	loc := constant.LocMap[server]
	t = t.In(loc)
	return t.Hour() == constant.GameDayStartHour &&
		t.Minute() == constant.GameDayStartMinute &&
		t.Second() == constant.GameDayStartSecond &&
		t.Nanosecond() == constant.GameDayStartNano
}
