package service

import (
	"context"
	"time"

	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
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

// DropMatrix

func (s *DropReport) CalcQuantityUniqCount(
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.QuantityUniqCountResultForDropMatrix, error) {
	results, err := s.DropReportRepo.CalcQuantityUniqCount(ctx, queryCtx)
	if err != nil {
		return nil, err
	}
	if queryCtx.StageItemFilter == nil {
		return results, nil
	}
	// filter the results by stageIdItemId map, because in repo layer we only filter by stageId
	filteredResults := make([]*model.QuantityUniqCountResultForDropMatrix, 0)
	resultsMapByStageId := make(map[int][]*model.QuantityUniqCountResultForDropMatrix)
	for _, result := range results {
		if _, ok := resultsMapByStageId[result.StageID]; !ok {
			resultsMapByStageId[result.StageID] = make([]*model.QuantityUniqCountResultForDropMatrix, 0)
		}
		resultsMapByStageId[result.StageID] = append(resultsMapByStageId[result.StageID], result)
	}
	for stageId, stageResults := range resultsMapByStageId {
		itemIdsSet := queryCtx.GetItemIdsSet(stageId)
		if itemIdsSet == nil {
			filteredResults = append(filteredResults, stageResults...)
		} else {
			for _, result := range stageResults {
				if _, ok := itemIdsSet[result.ItemID]; ok {
					filteredResults = append(filteredResults, result)
				}
			}
		}
	}
	return filteredResults, nil
}

func (s *DropReport) CalcTotalTimesForDropMatrix(
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.TotalTimesResult, error) {
	return s.DropReportRepo.CalcTotalTimes(ctx, queryCtx)
}

// PatternMatrix

func (s *DropReport) CalcTotalQuantityForPatternMatrix(
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.TotalQuantityResultForPatternMatrix, error) {
	return s.DropReportRepo.CalcTotalQuantityForPatternMatrix(ctx, queryCtx)
}

func (s *DropReport) CalcTotalTimesForPatternMatrix(
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.TotalTimesResult, error) {
	return s.DropReportRepo.CalcTotalTimes(ctx, queryCtx)
}

// Trend

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

// Sitestats

func (s *DropReport) CalcTotalStageQuantityForShimSiteStats(ctx context.Context, server string, isRecent24h bool) ([]*modelv2.TotalStageTime, error) {
	return s.DropReportRepo.CalcTotalStageQuantityForShimSiteStats(ctx, server, isRecent24h)
}

// Others

func (s *DropReport) CalcRecentUniqueUserCountBySource(ctx context.Context, duration time.Duration) ([]*modelv2.UniqueUserCountBySource, error) {
	return s.DropReportRepo.CalcRecentUniqueUserCountBySource(ctx, duration)
}
