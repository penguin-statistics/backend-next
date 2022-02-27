package gamedata

import (
	"time"

	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/models"
)

type BrandNewEventContext struct {
	ArkZoneID    string
	ZoneName     string
	ZoneCategory string
	ZoneType     *null.String
	Server       string
	StartTime    *time.Time
	EndTime      *time.Time
}

type BrandNewEventObjects struct {
	Zone      *models.Zone       `json:"zone"`
	Stages    []*models.Stage    `json:"stages"`
	DropInfos []*models.DropInfo `json:"dropInfos"`
	TimeRange *models.TimeRange  `json:"timeRange"`
	Activity  *models.Activity   `json:"activity"`
}
