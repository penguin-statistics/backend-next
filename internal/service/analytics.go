package service

import (
	"context"

	v2 "github.com/penguin-statistics/backend-next/internal/model/v2"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type Analytics struct {
	DropReportRepo *repo.DropReport
}

func NewAnalytics(dropReportRepo *repo.DropReport) *Analytics {
	return &Analytics{
		DropReportRepo: dropReportRepo,
	}
}

func (s *Analytics) GetRecentUniqueUserCountBySource(ctx context.Context, duration string) (map[string]int, error) {
	uniqueUserCount, err := s.DropReportRepo.CalcRecentUniqueUserCountBySource(ctx, duration)
	if err != nil {
		return nil, err
	}
	return s.convertUniqueUserCountToMap(uniqueUserCount), nil
}

func (s *Analytics) convertUniqueUserCountToMap(uniqueUserCount []*v2.UniqueUserCountBySource) map[string]int {
	result := make(map[string]int)
	for _, c := range uniqueUserCount {
		result[c.SourceName] = c.Count
	}
	return result
}
