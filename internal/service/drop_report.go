package service

import (
	"context"
	"time"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/coldstore"
	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/repo"
)

type DropReport struct {
	DropReportRepo *repo.DropReport
	// ColdDropReportRepo, when not nil, serves the archived part of the time ranges of the drop matrix and of the
	// pattern matrix (see CalcDropMatrixForIntervalsBySourceCategories and
	// CalcTotalQuantityForPatternMatrixBySourceCategories).
	ColdDropReportRepo *repo.ColdDropReport
}

func NewDropReport(dropReportRepo *repo.DropReport, coldDropReportRepo *repo.ColdDropReport) *DropReport {
	return &DropReport{
		DropReportRepo:     dropReportRepo,
		ColdDropReportRepo: coldDropReportRepo,
	}
}

// RunWithStatementTimeout runs fn with a context in which PostgreSQL cancels the drop report aggregations still running
// timeout after it was called. Each aggregation runs in a transaction of its own; fn holds no connection between them.
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

// CalcDropMatrixForIntervalsBySourceCategories, with the cold storage enabled for queries (ColdDropReportRepo), reads
// the part of each interval before the hot boundary from the cold storage and the rest from PostgreSQL, and adds
// the two up.
func (s *DropReport) CalcDropMatrixForIntervalsBySourceCategories(
	ctx context.Context, queryCtxs []*model.DropReportQueryContext, sourceCategories []string,
) ([]map[string][]*model.TotalTimesResult, []map[string][]*model.QuantityUniqCountResultForDropMatrix, error) {
	timesResults, quantityUniqCountResults, err := s.calcDropMatrixForIntervals(ctx, queryCtxs, sourceCategories)
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

func (s *DropReport) calcDropMatrixForIntervals(
	ctx context.Context, queryCtxs []*model.DropReportQueryContext, sourceCategories []string,
) ([]map[string][]*model.TotalTimesResult, []map[string][]*model.QuantityUniqCountResultForDropMatrix, error) {
	if s.ColdDropReportRepo == nil || len(queryCtxs) == 0 {
		return s.DropReportRepo.CalcDropMatrixForIntervalsBySourceCategories(ctx, queryCtxs, sourceCategories)
	}
	view := s.ColdDropReportRepo.View()
	cold, hot := splitQueryContexts(view, queryCtxs)
	if len(cold.queryCtxs) == 0 {
		return s.DropReportRepo.CalcDropMatrixForIntervalsBySourceCategories(ctx, queryCtxs, sourceCategories)
	}

	timesResults := make([]map[string][]*model.TotalTimesResult, len(queryCtxs))
	quantityUniqCountResults := make([]map[string][]*model.QuantityUniqCountResultForDropMatrix, len(queryCtxs))
	for i := range queryCtxs {
		timesResults[i] = make(map[string][]*model.TotalTimesResult, len(sourceCategories))
		quantityUniqCountResults[i] = make(map[string][]*model.QuantityUniqCountResultForDropMatrix, len(sourceCategories))
		for _, sourceCategory := range sourceCategories {
			timesResults[i][sourceCategory] = make([]*model.TotalTimesResult, 0)
			quantityUniqCountResults[i][sourceCategory] = make([]*model.QuantityUniqCountResultForDropMatrix, 0)
		}
	}
	add := func(part splitQueryContextsPart, partTimes []map[string][]*model.TotalTimesResult, partQuantities []map[string][]*model.QuantityUniqCountResultForDropMatrix) {
		for j, i := range part.indexes {
			for _, sourceCategory := range sourceCategories {
				timesResults[i][sourceCategory] = addTotalTimesResults(timesResults[i][sourceCategory], partTimes[j][sourceCategory])
				quantityUniqCountResults[i][sourceCategory] = addQuantityUniqCountResults(quantityUniqCountResults[i][sourceCategory], partQuantities[j][sourceCategory])
			}
		}
	}
	if len(hot.queryCtxs) > 0 {
		hotTimes, hotQuantities, err := s.DropReportRepo.CalcDropMatrixForIntervalsBySourceCategories(ctx, hot.queryCtxs, sourceCategories)
		if err != nil {
			return nil, nil, err
		}
		add(hot, hotTimes, hotQuantities)
	}
	coldTimes, coldQuantities, err := s.ColdDropReportRepo.CalcDropMatrixForIntervalsBySourceCategories(ctx, view, cold.queryCtxs, sourceCategories)
	if err != nil {
		return nil, nil, err
	}
	add(cold, coldTimes, coldQuantities)
	return timesResults, quantityUniqCountResults, nil
}

// splitQueryContextsPart is the part before or from the hot boundary of some of the intervals of a query.
type splitQueryContextsPart struct {
	queryCtxs []*model.DropReportQueryContext
	// indexes are the positions of the intervals of queryCtxs in the query
	indexes []int
}

// splitQueryContexts splits every interval at the hot boundary of view, leaving out empty parts. Intervals are cut
// to whole seconds first, as PostgreSQL compares them (see repo.DropReport.handleCreatedAtWithTime).
func splitQueryContexts(view *coldstore.View, queryCtxs []*model.DropReportQueryContext) (cold, hot splitQueryContextsPart) {
	for i, queryCtx := range queryCtxs {
		coldRange, hotRange := view.Split(time.Unix(queryCtx.StartTime.Unix(), 0), time.Unix(queryCtx.EndTime.Unix(), 0))
		for _, p := range []struct {
			part *splitQueryContextsPart
			r    coldstore.TimeRange
		}{{&cold, coldRange}, {&hot, hotRange}} {
			if p.r.Empty() {
				continue
			}
			partCtx := *queryCtx
			partCtx.StartTime, partCtx.EndTime = &p.r.Start, &p.r.End
			p.part.queryCtxs = append(p.part.queryCtxs, &partCtx)
			p.part.indexes = append(p.part.indexes, i)
		}
	}
	return cold, hot
}

// addTotalTimesResults adds the times of b to those of the same stage in a, and appends the other stages of b.
func addTotalTimesResults(a, b []*model.TotalTimesResult) []*model.TotalTimesResult {
	byStage := make(map[int]*model.TotalTimesResult, len(a))
	for _, result := range a {
		byStage[result.StageID] = result
	}
	for _, result := range b {
		if sum, ok := byStage[result.StageID]; ok {
			sum.TotalTimes += result.TotalTimes
			continue
		}
		sum := *result
		byStage[result.StageID] = &sum
		a = append(a, &sum)
	}
	return a
}

// addQuantityUniqCountResults adds the counts of b to those of the same stage, item and quantity in a, and appends
// the others of b.
func addQuantityUniqCountResults(a, b []*model.QuantityUniqCountResultForDropMatrix) []*model.QuantityUniqCountResultForDropMatrix {
	type key struct{ stageId, itemId, quantity int }
	byKey := make(map[key]*model.QuantityUniqCountResultForDropMatrix, len(a))
	for _, result := range a {
		byKey[key{result.StageID, result.ItemID, result.Quantity}] = result
	}
	for _, result := range b {
		k := key{result.StageID, result.ItemID, result.Quantity}
		if sum, ok := byKey[k]; ok {
			sum.Count += result.Count
			continue
		}
		sum := *result
		byKey[k] = &sum
		a = append(a, &sum)
	}
	return a
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

// CalcTotalQuantityForPatternMatrixBySourceCategories, with the cold storage enabled for queries
// (ColdDropReportRepo), reads the part of the interval before the hot boundary from the cold storage and the rest
// from PostgreSQL, and adds the two up.
func (s *DropReport) CalcTotalQuantityForPatternMatrixBySourceCategories(
	ctx context.Context, queryCtx *model.DropReportQueryContext, sourceCategories []string,
) (map[string][]*model.TotalQuantityResultForPatternMatrix, error) {
	if s.ColdDropReportRepo == nil {
		return s.DropReportRepo.CalcTotalQuantityForPatternMatrixBySourceCategories(ctx, queryCtx, sourceCategories)
	}
	view := s.ColdDropReportRepo.View()
	cold, hot := splitQueryContexts(view, []*model.DropReportQueryContext{queryCtx})
	if len(cold.queryCtxs) == 0 {
		return s.DropReportRepo.CalcTotalQuantityForPatternMatrixBySourceCategories(ctx, queryCtx, sourceCategories)
	}

	results, err := s.ColdDropReportRepo.CalcTotalQuantityForPatternMatrixBySourceCategories(ctx, view, cold.queryCtxs[0], sourceCategories)
	if err != nil {
		return nil, err
	}
	if len(hot.queryCtxs) > 0 {
		hotResults, err := s.DropReportRepo.CalcTotalQuantityForPatternMatrixBySourceCategories(ctx, hot.queryCtxs[0], sourceCategories)
		if err != nil {
			return nil, err
		}
		for _, sourceCategory := range sourceCategories {
			results[sourceCategory] = addPatternQuantityResults(results[sourceCategory], hotResults[sourceCategory])
		}
	}
	return results, nil
}

// addPatternQuantityResults adds the quantities of b to those of the same stage and pattern in a, and appends the
// others of b.
func addPatternQuantityResults(a, b []*model.TotalQuantityResultForPatternMatrix) []*model.TotalQuantityResultForPatternMatrix {
	type key struct{ stageId, patternId int }
	byKey := make(map[key]*model.TotalQuantityResultForPatternMatrix, len(a))
	for _, result := range a {
		byKey[key{result.StageID, result.PatternID}] = result
	}
	for _, result := range b {
		k := key{result.StageID, result.PatternID}
		if sum, ok := byKey[k]; ok {
			sum.TotalQuantity += result.TotalQuantity
			continue
		}
		sum := *result
		byKey[k] = &sum
		a = append(a, &sum)
	}
	return a
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
