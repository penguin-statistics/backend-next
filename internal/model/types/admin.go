package types

import (
	"encoding/json"
	"time"

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

type RejectRulesReevaluationPreviewRequest struct {
	RuleID          int `json:"ruleId"`
	ReevaluateRange struct {
		From time.Time `json:"from"`
		To   time.Time `json:"to"`
	} `json:"reevaluateRange"`
}

type CloneFromCNRequest struct {
	ArkZoneID        string           `json:"arkZoneId" validate:"required" required:"true"`
	RangeID          int              `json:"rangeId" validate:"required" required:"true"`
	ActivityID       int              `json:"activityId"`
	ForeignZoneName  json.RawMessage  `json:"foreignZoneName"`
	ForeignTimeRange ForeignTimeRange `json:"foreignTimeRange"`
}

type ArchiveDropReportRequest struct {
	Date string `json:"date" validate:"required" required:"true"`
}

type ForeignTimeRange struct {
	US ForeignTimeRangeString `json:"US"`
	JP ForeignTimeRangeString `json:"JP"`
	KR ForeignTimeRangeString `json:"KR"`
}

type ForeignTimeRangeString struct {
	Start string `json:"start"`
	End   string `json:"end"`
}
