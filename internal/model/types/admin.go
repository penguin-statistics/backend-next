package types

import (
	"gopkg.in/guregu/null.v3"
)

type UpdateNewEventRequest struct {
	ArkZoneId    string      `json:"arkZoneId"`
	ZoneName     string      `json:"zoneName"`
	ZoneCategory string      `json:"zoneCategory"`
	ZoneType     null.String `json:"zoneType" swaggertype:"string"`
	Server       string      `json:"server"`

	TimeRange
}

type CloneEventRequest struct {
	ZonePrefix string                `json:"zonePrefix"`
	FromServer string                `json:"fromServer"`
	ToServers  []string              `json:"toServers"`
	TimeRanges map[string]*TimeRange `json:"timeRanges"`
	NameMap    map[string]string     `json:"nameMap"`
}

type TimeRange struct {
	StartTime string      `json:"startTime"`
	EndTime   null.String `json:"endTime" swaggertype:"string"`
}

type PurgeCacheRequest struct {
	Pairs []PurgeCachePair `json:"pairs"`
}

type PurgeCachePair struct {
	Name string      `json:"name"`
	Key  null.String `json:"key" swaggertype:"string"`
}
