package service

import (
	"context"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/pkg/errors"

	v2 "exusiai.dev/backend-next/internal/model/v2"
)

type Analytics struct {
	DropReportService *DropReport
}

func NewAnalytics(dropReportService *DropReport) *Analytics {
	return &Analytics{
		DropReportService: dropReportService,
	}
}

func (s *Analytics) GetRecentUniqueUserCountBySource(ctx context.Context, recent string) (map[string]int, error) {
	duration, err := time.ParseDuration(recent)
	if err != nil {
		return nil, err
	}
	maxDuration, err := time.ParseDuration(constant.MaxRecentDuration)
	if err != nil {
		return nil, err
	}
	if duration > maxDuration {
		return nil, errors.New("duration is too long")
	}
	uniqueUserCount, err := s.DropReportService.CalcRecentUniqueUserCountBySource(ctx, duration)
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
