package service

import (
	"context"
	"time"

	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type DropReportService struct {
	DropReportRepo *repo.DropReport
}

func NewDropReportService(dropReportRepo *repo.DropReport) *DropReportService {
	return &DropReportService{
		DropReportRepo: dropReportRepo,
	}
}

func (s *DropReportService) CalcTotalQuantityForDropMatrix(ctx context.Context, server string, timeRange *models.TimeRange, stageIdItemIdMap map[int][]int, accountId null.Int) ([]*models.TotalQuantityResultForDropMatrix, error) {
	return s.DropReportRepo.CalcTotalQuantityForDropMatrix(ctx, server, timeRange, stageIdItemIdMap, accountId)
}

func (s *DropReportService) CalcTotalQuantityForPatternMatrix(ctx context.Context, server string, timeRange *models.TimeRange, stageIds []int, accountId null.Int) ([]*models.TotalQuantityResultForPatternMatrix, error) {
	return s.DropReportRepo.CalcTotalQuantityForPatternMatrix(ctx, server, timeRange, stageIds, accountId)
}

func (s *DropReportService) CalcTotalTimesForDropMatrix(ctx context.Context, server string, timeRange *models.TimeRange, stageIds []int, accountId null.Int) ([]*models.TotalTimesResult, error) {
	return s.DropReportRepo.CalcTotalTimes(ctx, server, timeRange, stageIds, accountId, false)
}

func (s *DropReportService) CalcTotalTimesForPatternMatrix(ctx context.Context, server string, timeRange *models.TimeRange, stageIds []int, accountId null.Int) ([]*models.TotalTimesResult, error) {
	return s.DropReportRepo.CalcTotalTimes(ctx, server, timeRange, stageIds, accountId, true)
}

func (s *DropReportService) CalcTotalQuantityForTrend(ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIdItemIdMap map[int][]int, accountId null.Int) ([]*models.TotalQuantityResultForTrend, error) {
	return s.DropReportRepo.CalcTotalQuantityForTrend(ctx, server, startTime, intervalLength, intervalNum, stageIdItemIdMap, accountId)
}

func (s *DropReportService) CalcTotalTimesForTrend(ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIds []int, accountId null.Int) ([]*models.TotalTimesResultForTrend, error) {
	return s.DropReportRepo.CalcTotalTimesForTrend(ctx, server, startTime, intervalLength, intervalNum, stageIds, accountId)
}
