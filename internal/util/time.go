package util

import (
	"time"

	"exusiai.dev/gommon/constant"
)

// floor to 12am of the day of the given time and given server
func GetDayStartTime(t *time.Time, server string) int64 {
	loc := constant.LocMap[server]
	localT := t.In(loc)
	newT := time.Date(localT.Year(), localT.Month(), localT.Day(), 0, 0, 0, 0, loc)
	return newT.UnixMilli()
}

func GetDayNum(t *time.Time, server string) int {
	serverOpenStartTimeLong := constant.ServerStartTimeMapMillis[server]
	serverOpenStartTime := time.UnixMilli(serverOpenStartTimeLong)
	return int((GetDayStartTime(t, server) - GetDayStartTime(&serverOpenStartTime, server)) / 86400000)
}

func GetDayStartTimestampFromDayNum(dayNum int, server string) int64 {
	serverOpenStartTimeLong := constant.ServerStartTimeMapMillis[server]
	serverOpenStartTime := time.UnixMilli(serverOpenStartTimeLong)
	return GetDayStartTime(&serverOpenStartTime, server) + int64(dayNum)*86400000
}
