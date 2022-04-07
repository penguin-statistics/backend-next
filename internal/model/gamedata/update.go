package gamedata

import (
	"github.com/penguin-statistics/backend-next/internal/core/activity"
	"github.com/penguin-statistics/backend-next/internal/model"
)

type RenderedObjects struct {
	Zone         *model.Zone                  `json:"zone"`
	Stages       []*model.Stage               `json:"stages"`
	DropInfosMap map[string][]*model.DropInfo `json:"dropInfosMap"`
	TimeRange    *model.TimeRange             `json:"timeRange"`
	Activity     *activity.Model              `json:"activity"`
}
