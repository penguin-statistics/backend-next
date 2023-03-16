package model

import (
	"time"

	"gopkg.in/guregu/null.v3"
)

type DropReportQueryContext struct {
	Server             string         `json:"server"`
	StartTime          *time.Time     `json:"startTime"`
	EndTime            *time.Time     `json:"endTime"`
	AccountID          null.Int       `json:"accountId"`
	StageItemFilter    *map[int][]int `json:"stageItemFilter"`
	SourceCategory     string         `json:"sourceCategory"`
	ExcludeNonOneTimes bool           `json:"excludeNonOneTimes"`
}

func (queryCtx *DropReportQueryContext) GetStageIds() []int {
	if queryCtx.StageItemFilter == nil {
		return make([]int, 0)
	}
	stageIds := make([]int, len(*queryCtx.StageItemFilter))
	i := 0
	for k := range *queryCtx.StageItemFilter {
		stageIds[i] = k
		i++
	}
	return stageIds
}
