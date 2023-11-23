package types

import "gopkg.in/guregu/null.v3"

type AdvancedQueryRequest struct {
	Queries []*AdvancedQuery `json:"queries" validate:"required,max=5,min=1,dive"`
}

type AdvancedQuery struct {
	Server         string    `json:"server" validate:"required,arkserver" required:"true"`
	StageID        string    `json:"stageId" validate:"required" required:"true"`
	ItemIDs        []string  `json:"itemIds"`
	IsPersonal     null.Bool `json:"isPersonal" swaggertype:"boolean"`
	SourceCategory string    `json:"sourceCategory" validate:"sourcecategory"`
	StartTime      int64     `json:"start" swaggertype:"integer"`
	EndTime        int64     `json:"end" validate:"omitempty,gtfield=StartTime" swaggertype:"integer"`
	Interval       null.Int  `json:"interval" swaggertype:"integer"`
}
