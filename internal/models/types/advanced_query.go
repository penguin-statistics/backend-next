package types

import "gopkg.in/guregu/null.v3"

type AdvancedQueryRequest struct {
	Queries []*AdvancedQuery `json:"queries" validate:"required,max=5,min=1,dive"`
}

type AdvancedQuery struct {
	Server     string     `json:"server" validate:"required,caseinsensitiveoneof=CN US JP KR" required:"true"`
	StageID    string     `json:"stageId" validate:"required" required:"true"`
	ItemIDs    []string   `json:"itemIds"`
	IsPersonal *null.Bool `json:"isPersonal"`
	StartTime  *null.Int  `json:"start"`
	EndTime    *null.Int  `json:"end"`
	Interval   *null.Int  `json:"interval"`
}
