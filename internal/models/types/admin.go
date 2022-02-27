package types

import "gopkg.in/guregu/null.v3"

type UpdateBrandNewEventRequest struct {
	ArkZoneID    string       `json:"arkZoneId"`
	ZoneName     string       `json:"zoneName"`
	ZoneCategory string       `json:"zoneCategory"`
	ZoneType     *null.String `json:"zoneType"`
	Server       string       `json:"server"`
	StartTime    string       `json:"startTime"`
	EndTime      *null.String `json:"endTime"`
}
