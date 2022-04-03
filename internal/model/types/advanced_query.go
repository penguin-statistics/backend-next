package types

import "gopkg.in/guregu/null.v3"

type AdvancedQueryRequest struct {
	Queries []*AdvancedQuery `json:"queries" validate:"required,max=5,min=1,dive"`
}

type AdvancedQuery struct {
	Server     string    `json:"server" validate:"required,alpha,caseinsensitiveoneof=CN US JP KR" required:"true"`
	StageID    string    `json:"stageId" validate:"required" required:"true"`
	ItemIDs    []string  `json:"itemIds"`
	IsPersonal null.Bool `json:"isPersonal" swaggertype:"boolean"`
	StartTime  null.Int  `json:"start" swaggertype:"integer"`
	EndTime    null.Int  `json:"end" swaggertype:"integer"`
	Interval   null.Int  `json:"interval" swaggertype:"integer"`
}
