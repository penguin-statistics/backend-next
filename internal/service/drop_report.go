package service

import (
	"context"
	"time"

	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type DropReport struct {
	DropReportRepo *repo.DropReport
}

func NewDropReport(dropReportRepo *repo.DropReport) *DropReport {
	return &DropReport{
		DropReportRepo: dropReportRepo,
	}
}

func (s *DropReport) CalcTotalQuantityForDropMatrix(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIdItemIdMap map[int][]int, accountId null.Int, sourceCategory string,
) ([]*model.TotalQuantityResultForDropMatrix, error) {
	return s.DropReportRepo.CalcTotalQuantityForDropMatrix(ctx, server, timeRange, stageIdItemIdMap, accountId, sourceCategory)
}

func (s *DropReport) CalcTotalQuantityForPatternMatrix(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIds []int, accountId null.Int, sourceCategory string,
) ([]*model.TotalQuantityResultForPatternMatrix, error) {
	return s.DropReportRepo.CalcTotalQuantityForPatternMatrix(ctx, server, timeRange, stageIds, accountId, sourceCategory)
}

func (s *DropReport) CalcTotalTimesForDropMatrix(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIds []int, accountId null.Int, sourceCategory string,
) ([]*model.TotalTimesResult, error) {
	return s.DropReportRepo.CalcTotalTimes(ctx, server, timeRange, stageIds, accountId, false, sourceCategory)
}

func (s *DropReport) CalcTotalTimesForPatternMatrix(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIds []int, accountId null.Int, sourceCategory string,
) ([]*model.TotalTimesResult, error) {
	return s.DropReportRepo.CalcTotalTimes(ctx, server, timeRange, stageIds, accountId, true, sourceCategory)
}

func (s *DropReport) CalcTotalQuantityForTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIdItemIdMap map[int][]int, accountId null.Int, sourceCategory string,
) ([]*model.TotalQuantityResultForTrend, error) {
	return s.DropReportRepo.CalcTotalQuantityForTrend(ctx, server, startTime, intervalLength, intervalNum, stageIdItemIdMap, accountId, sourceCategory)
}

func (s *DropReport) CalcTotalTimesForTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIds []int, accountId null.Int, sourceCategory string,
) ([]*model.TotalTimesResultForTrend, error) {
	return s.DropReportRepo.CalcTotalTimesForTrend(ctx, server, startTime, intervalLength, intervalNum, stageIds, accountId, sourceCategory)
}

func (s *DropReport) CalcQuantityUniqCount(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIdItemIdMap map[int][]int, accountId null.Int, sourceCategory string,
) ([]*model.QuantityUniqCountResultForDropMatrix, error) {
	return s.DropReportRepo.CalcQuantityUniqCount(ctx, server, timeRange, stageIdItemIdMap, accountId, sourceCategory)
}
