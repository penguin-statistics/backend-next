package v3

import (
	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
)

type AggregatedItemStats struct {
	Matrix []*modelv2.OneDropMatrixElement `json:"matrix"`
	Trends map[string]*modelv2.StageTrend  `json:"trends"`
}

type AggregatedStageStats struct {
	Matrix   []*modelv2.OneDropMatrixElement    `json:"matrix"`
	Trends   map[string]*modelv2.StageTrend     `json:"trends"`
	Patterns []*modelv2.OnePatternMatrixElement `json:"patterns"`
}
