package v3

import (
	"encoding/json"

	"gopkg.in/guregu/null.v3"
)

type Stage struct {
	StageID          int             `bun:",pk,autoincrement" json:"pgStageId"`
	ArkStageID       string          `json:"arkStageId"`
	ZoneID           int             `json:"zoneId"`
	StageType        string          `json:"stageType"`
	ExtraProcessType null.String     `json:"extraProcessType,omitempty" swaggertype:"string"`
	Code             json.RawMessage `json:"code"`
	Sanity           null.Int        `json:"sanity" swaggertype:"integer,x-nullable"`
	Existence        json.RawMessage `json:"existence" swaggertype:"object"`
	MinClearTime     null.Int        `json:"minClearTime" swaggertype:"integer,x-nullable"`
}
