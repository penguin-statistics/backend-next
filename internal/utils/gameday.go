package utils

import (
	"time"

	"github.com/penguin-statistics/backend-next/internal/constants"
)

func GetGameDayStartTime(server string, t time.Time) time.Time {
	loc := constants.LocMap[server]
	t = t.In(loc)
	if t.Hour() < 4 {
		t = t.Add(time.Hour * -24)
	}
	newT := time.Date(t.Year(), t.Month(), t.Day(), constants.GameDayStartHour, constants.GameDayStartMinute, constants.GameDayStartSecond, constants.GameDayStartNano, loc)
	return newT
}

func GetGameDayEndTime(server string, t time.Time) time.Time {
	return GetGameDayStartTime(server, t).Add(time.Hour * 24)
}

func IsGameDayStartTime(server string, t time.Time) bool {
	loc := constants.LocMap[server]
	t = t.In(loc)
	return t.Hour() == constants.GameDayStartHour &&
		t.Minute() == constants.GameDayStartMinute &&
		t.Second() == constants.GameDayStartSecond &&
		t.Nanosecond() == constants.GameDayStartNano
}
