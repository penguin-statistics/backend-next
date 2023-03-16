package service

import (
	"context"
	"time"

	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo"
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
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.TotalQuantityResultForDropMatrix, error) {
	return s.DropReportRepo.CalcTotalQuantityForDropMatrix(ctx, queryCtx)
}

func (s *DropReport) CalcTotalQuantityForPatternMatrix(
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.TotalQuantityResultForPatternMatrix, error) {
	return s.DropReportRepo.CalcTotalQuantityForPatternMatrix(ctx, queryCtx)
}

func (s *DropReport) CalcTotalTimesForDropMatrix(
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.TotalTimesResult, error) {
	return s.DropReportRepo.CalcTotalTimes(ctx, queryCtx)
}

func (s *DropReport) CalcTotalTimesForPatternMatrix(
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.TotalTimesResult, error) {
	return s.DropReportRepo.CalcTotalTimes(ctx, queryCtx)
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
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.QuantityUniqCountResultForDropMatrix, error) {
	return s.DropReportRepo.CalcQuantityUniqCount(ctx, queryCtx)
}
