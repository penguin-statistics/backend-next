package gamedata

import (
	"exusiai.dev/backend-next/internal/model"
)

type RenderedObjects struct {
	Zone         *model.Zone                  `json:"zone"`
	Stages       []*model.Stage               `json:"stages"`
	DropInfosMap map[string][]*model.DropInfo `json:"dropInfosMap"`
	TimeRange    *model.TimeRange             `json:"timeRange"`
	Activity     *model.Activity              `json:"activity"`
}
