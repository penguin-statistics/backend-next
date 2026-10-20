package service

import (
	"context"
	"time"

	"github.com/uptrace/bun"
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

// RunWithStatementTimeout runs fn in a transaction in which PostgreSQL cancels every statement running
// longer than timeout. The drop report aggregations called with the context fn receives run in that transaction.
func (s *DropReport) RunWithStatementTimeout(ctx context.Context, timeout time.Duration, fn func(ctx context.Context) error) error {
	return s.DropReportRepo.RunWithStatementTimeout(ctx, timeout, fn)
}

// DropMatrix

func (s *DropReport) CalcQuantityUniqCount(
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.QuantityUniqCountResultForDropMatrix, error) {
	results, err := s.DropReportRepo.CalcQuantityUniqCount(ctx, queryCtx)
	if err != nil {
		return nil, err
	}
	return filterQuantityUniqCountResultsByItems(queryCtx, results), nil
}

func (s *DropReport) CalcDropMatrixForIntervalsBySourceCategories(
	ctx context.Context, queryCtxs []*model.DropReportQueryContext, sourceCategories []string,
) ([]map[string][]*model.TotalTimesResult, []map[string][]*model.QuantityUniqCountResultForDropMatrix, error) {
	timesResults, quantityUniqCountResults, err := s.DropReportRepo.CalcDropMatrixForIntervalsBySourceCategories(ctx, queryCtxs, sourceCategories)
	if err != nil {
		return nil, nil, err
	}
	for i, quantityUniqCountsBySourceCategory := range quantityUniqCountResults {
		for sourceCategory, results := range quantityUniqCountsBySourceCategory {
			quantityUniqCountsBySourceCategory[sourceCategory] = filterQuantityUniqCountResultsByItems(queryCtxs[i], results)
		}
	}
	return timesResults, quantityUniqCountResults, nil
}

// filter the results by stageIdItemId map, because in repo layer we only filter by stageId
func filterQuantityUniqCountResultsByItems(
	queryCtx *model.DropReportQueryContext, results []*model.QuantityUniqCountResultForDropMatrix,
) []*model.QuantityUniqCountResultForDropMatrix {
	if queryCtx.StageItemFilter == nil {
		return results
	}
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
	return filteredResults
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

func (s *DropReport) CalcTotalQuantityForPatternMatrixBySourceCategories(
	ctx context.Context, queryCtx *model.DropReportQueryContext, sourceCategories []string,
) (map[string][]*model.TotalQuantityResultForPatternMatrix, error) {
	return s.DropReportRepo.CalcTotalQuantityForPatternMatrixBySourceCategories(ctx, queryCtx, sourceCategories)
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

func (s *DropReport) GetDropReports(
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.DropReport, error) {
	return s.DropReportRepo.GetDropReports(ctx, queryCtx)
}

// Archive

func (s *DropReport) GetDropReportsForArchive(ctx context.Context, cursor *model.Cursor, date time.Time, limit int) ([]*model.DropReport, model.Cursor, error) {
	return s.DropReportRepo.GetDropReportsForArchive(ctx, cursor, date, limit)
}

func (s *DropReport) DeleteDropReportsForArchive(ctx context.Context, tx bun.Tx, date time.Time) (int64, error) {
	return s.DropReportRepo.DeleteDropReportsForArchive(ctx, tx, date)
}
