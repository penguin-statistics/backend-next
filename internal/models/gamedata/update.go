package gamedata

import (
	"time"

	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/models"
)

type NewEventBasicInfo struct {
	ArkZoneID    string
	ZoneName     string
	ZoneCategory string
	ZoneType     null.String
	Server       string
	StartTime    *time.Time
	EndTime      *time.Time
}

type RenderedObjects struct {
	Zone         *models.Zone                  `json:"zone"`
	Stages       []*models.Stage               `json:"stages"`
	DropInfosMap map[string][]*models.DropInfo `json:"dropInfosMap"`
	TimeRange    *models.TimeRange             `json:"timeRange"`
	Activity     *models.Activity              `json:"activity"`
}
