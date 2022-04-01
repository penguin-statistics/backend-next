package gamedata

import (
	"github.com/penguin-statistics/backend-next/internal/models"
)

type RenderedObjects struct {
	Zone         *models.Zone                  `json:"zone"`
	Stages       []*models.Stage               `json:"stages"`
	DropInfosMap map[string][]*models.DropInfo `json:"dropInfosMap"`
	TimeRange    *models.TimeRange             `json:"timeRange"`
	Activity     *models.Activity              `json:"activity"`
}
