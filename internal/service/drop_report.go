package service

import (
	"context"
	"slices"
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
	// ColdDropReportRepo, when not nil, serves the archived part of the time ranges of the drop matrix, of the
	// pattern matrix and of the customized trend (see CalcDropMatrixForIntervalsBySourceCategories,
	// CalcTotalQuantityForPatternMatrixBySourceCategories and CalcTotalQuantityForTrend). In shadow mode
	// (ColdstoreShadowEnabled), those answer from PostgreSQL alone and compare the split results in the background.
	ColdDropReportRepo *repo.ColdDropReport

	// shadow is not nil in shadow mode
	shadow *coldShadow
}

func NewDropReport(dropReportRepo *repo.DropReport, coldDropReportRepo *repo.ColdDropReport) *DropReport {
	s := &DropReport{
		DropReportRepo:     dropReportRepo,
		ColdDropReportRepo: coldDropReportRepo,
	}
	if coldDropReportRepo != nil && coldDropReportRepo.Shadow().Enabled {
		s.shadow = newColdShadow(coldDropReportRepo.Shadow(), dropReportRepo.RunWithStatementTimeout)
	}
	return s
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
	if s.shadow != nil {
		shadowCtxs, shadowCategories := cloneQueryContexts(queryCtxs), slices.Clone(sourceCategories)
		cold, hot := splitQueryContexts(view, shadowCtxs)
		results, err := runShadow(ctx, s.shadow, newDropMatrixShadowQuery("drop_matrix", view.HotBoundary(), queryCtxs, sourceCategories),
			func(ctx context.Context) (dropMatrixResults, error) {
				times, quantities, err := s.DropReportRepo.CalcDropMatrixForIntervalsBySourceCategories(ctx, queryCtxs, sourceCategories)
				return dropMatrixResults{times, quantities}, err
			},
			func(ctx context.Context) (dropMatrixResults, error) {
				times, quantities, err := s.calcDropMatrixSplit(ctx, view, len(shadowCtxs), cold, hot, shadowCategories)
				return dropMatrixResults{times, quantities}, err
			},
			dropMatrixShadowCounts)
		return results.times, results.quantities, err
	}
	return s.calcDropMatrixSplit(ctx, view, len(queryCtxs), cold, hot, sourceCategories)
}

// calcDropMatrixSplit adds up the drop matrix of the cold and hot parts of n intervals, split at the hot boundary of
// view.
func (s *DropReport) calcDropMatrixSplit(
	ctx context.Context, view *coldstore.View, n int, cold, hot splitQueryContextsPart, sourceCategories []string,
) ([]map[string][]*model.TotalTimesResult, []map[string][]*model.QuantityUniqCountResultForDropMatrix, error) {
	timesResults := make([]map[string][]*model.TotalTimesResult, n)
	quantityUniqCountResults := make([]map[string][]*model.QuantityUniqCountResultForDropMatrix, n)
	for i := 0; i < n; i++ {
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
	if s.shadow != nil {
		shadowCategories := slices.Clone(sourceCategories)
		cold, hot := splitQueryContexts(view, cloneQueryContexts([]*model.DropReportQueryContext{queryCtx}))
		return runShadow(ctx, s.shadow, newDropMatrixShadowQuery("pattern_matrix", view.HotBoundary(), []*model.DropReportQueryContext{queryCtx}, sourceCategories),
			func(ctx context.Context) (map[string][]*model.TotalQuantityResultForPatternMatrix, error) {
				return s.DropReportRepo.CalcTotalQuantityForPatternMatrixBySourceCategories(ctx, queryCtx, sourceCategories)
			},
			func(ctx context.Context) (map[string][]*model.TotalQuantityResultForPatternMatrix, error) {
				return s.calcPatternMatrixSplit(ctx, view, cold, hot, shadowCategories)
			},
			patternMatrixShadowCounts)
	}
	return s.calcPatternMatrixSplit(ctx, view, cold, hot, sourceCategories)
}

// calcPatternMatrixSplit adds up the pattern matrix of the cold and hot parts of an interval, split at the hot
// boundary of view.
func (s *DropReport) calcPatternMatrixSplit(
	ctx context.Context, view *coldstore.View, cold, hot splitQueryContextsPart, sourceCategories []string,
) (map[string][]*model.TotalQuantityResultForPatternMatrix, error) {
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

// CalcTotalQuantityForTrend, with the cold storage enabled for queries (ColdDropReportRepo), reads the reports before
// the hot boundary from the cold storage and the rest from PostgreSQL, and adds the two up per bucket. A bucket that
// straddles the boundary is counted from both.
func (s *DropReport) CalcTotalQuantityForTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIdItemIdMap map[int][]int, accountId null.Int, sourceCategory string,
) ([]*model.TotalQuantityResultForTrend, error) {
	if s.ColdDropReportRepo == nil || len(stageIdItemIdMap) == 0 {
		return s.DropReportRepo.CalcTotalQuantityForTrend(ctx, server, startTime, intervalLength, intervalNum, stageIdItemIdMap, accountId, sourceCategory)
	}
	view := s.ColdDropReportRepo.View()
	cold, hot, ok := splitTrend(view, server, *startTime, intervalLength, intervalNum)
	if !ok {
		return s.DropReportRepo.CalcTotalQuantityForTrend(ctx, server, startTime, intervalLength, intervalNum, stageIdItemIdMap, accountId, sourceCategory)
	}
	if s.shadow != nil {
		shadowStart, shadowMap := *startTime, cloneStageIdItemIdMap(stageIdItemIdMap)
		return runShadow(ctx, s.shadow, newTrendShadowQuery("trend_quantity", view.HotBoundary(), server, *startTime, intervalLength, intervalNum, cold, hot, accountId.Valid, len(stageIdItemIdMap), sourceCategory),
			func(ctx context.Context) ([]*model.TotalQuantityResultForTrend, error) {
				return s.DropReportRepo.CalcTotalQuantityForTrend(ctx, server, startTime, intervalLength, intervalNum, stageIdItemIdMap, accountId, sourceCategory)
			},
			func(ctx context.Context) ([]*model.TotalQuantityResultForTrend, error) {
				return s.calcTrendQuantitySplit(ctx, view, cold, hot, server, &shadowStart, intervalLength, intervalNum, shadowMap, accountId, sourceCategory)
			},
			trendQuantityShadowCounts)
	}
	return s.calcTrendQuantitySplit(ctx, view, cold, hot, server, startTime, intervalLength, intervalNum, stageIdItemIdMap, accountId, sourceCategory)
}

// calcTrendQuantitySplit adds up the trend quantities of the cold and hot parts of a trend, split at the hot boundary
// of view.
func (s *DropReport) calcTrendQuantitySplit(
	ctx context.Context, view *coldstore.View, cold, hot coldstore.TimeRange, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIdItemIdMap map[int][]int, accountId null.Int, sourceCategory string,
) ([]*model.TotalQuantityResultForTrend, error) {
	results, err := s.ColdDropReportRepo.CalcTotalQuantityForTrend(ctx, view, cold.Start, cold.End, server, startTime, intervalLength, intervalNum, stageIdItemIdMap, accountId, sourceCategory)
	if err != nil {
		return nil, err
	}
	if !hot.Empty() {
		hotResults, err := s.DropReportRepo.CalcTotalQuantityForTrendWithin(ctx, hot.Start, hot.End, server, startTime, intervalLength, intervalNum, stageIdItemIdMap, accountId, sourceCategory)
		if err != nil {
			return nil, err
		}
		results = addTrendQuantityResults(results, hotResults)
	}
	return results, nil
}

// CalcTotalTimesForTrend splits the trend at the hot boundary as CalcTotalQuantityForTrend.
func (s *DropReport) CalcTotalTimesForTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIds []int, accountId null.Int, sourceCategory string,
) ([]*model.TotalTimesResultForTrend, error) {
	if s.ColdDropReportRepo == nil || len(stageIds) == 0 {
		return s.DropReportRepo.CalcTotalTimesForTrend(ctx, server, startTime, intervalLength, intervalNum, stageIds, accountId, sourceCategory)
	}
	view := s.ColdDropReportRepo.View()
	cold, hot, ok := splitTrend(view, server, *startTime, intervalLength, intervalNum)
	if !ok {
		return s.DropReportRepo.CalcTotalTimesForTrend(ctx, server, startTime, intervalLength, intervalNum, stageIds, accountId, sourceCategory)
	}
	if s.shadow != nil {
		shadowStart, shadowStageIds := *startTime, slices.Clone(stageIds)
		return runShadow(ctx, s.shadow, newTrendShadowQuery("trend_times", view.HotBoundary(), server, *startTime, intervalLength, intervalNum, cold, hot, accountId.Valid, len(stageIds), sourceCategory),
			func(ctx context.Context) ([]*model.TotalTimesResultForTrend, error) {
				return s.DropReportRepo.CalcTotalTimesForTrend(ctx, server, startTime, intervalLength, intervalNum, stageIds, accountId, sourceCategory)
			},
			func(ctx context.Context) ([]*model.TotalTimesResultForTrend, error) {
				return s.calcTrendTimesSplit(ctx, view, cold, hot, server, &shadowStart, intervalLength, intervalNum, shadowStageIds, accountId, sourceCategory)
			},
			trendTimesShadowCounts)
	}
	return s.calcTrendTimesSplit(ctx, view, cold, hot, server, startTime, intervalLength, intervalNum, stageIds, accountId, sourceCategory)
}

// calcTrendTimesSplit adds up the trend times of the cold and hot parts of a trend, split at the hot boundary of view.
func (s *DropReport) calcTrendTimesSplit(
	ctx context.Context, view *coldstore.View, cold, hot coldstore.TimeRange, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIds []int, accountId null.Int, sourceCategory string,
) ([]*model.TotalTimesResultForTrend, error) {
	results, err := s.ColdDropReportRepo.CalcTotalTimesForTrend(ctx, view, cold.Start, cold.End, server, startTime, intervalLength, intervalNum, stageIds, accountId, sourceCategory)
	if err != nil {
		return nil, err
	}
	if !hot.Empty() {
		hotResults, err := s.DropReportRepo.CalcTotalTimesForTrendWithin(ctx, hot.Start, hot.End, server, startTime, intervalLength, intervalNum, stageIds, accountId, sourceCategory)
		if err != nil {
			return nil, err
		}
		results = addTrendTimesResults(results, hotResults)
	}
	return results, nil
}

// splitTrend splits the time range of the buckets of a trend at the hot boundary of view, in whole seconds as
// PostgreSQL compares them. It returns false when no part is cold, or when the interval length is shorter than an
// hour (PostgreSQL then fails the query as before).
func splitTrend(view *coldstore.View, server string, startTime time.Time, intervalLength time.Duration, intervalNum int) (cold, hot coldstore.TimeRange, ok bool) {
	if int(intervalLength.Hours()) <= 0 {
		return cold, hot, false
	}
	start, end := repo.TrendTimeRange(server, startTime, intervalLength, intervalNum)
	cold, hot = view.Split(time.Unix(start.Unix(), 0), time.Unix(end.Unix(), 0))
	return cold, hot, !cold.Empty()
}

// addTrendQuantityResults adds the quantities of b to those of the same bucket, stage and item in a, and appends the
// others of b.
func addTrendQuantityResults(a, b []*model.TotalQuantityResultForTrend) []*model.TotalQuantityResultForTrend {
	type key struct{ groupId, stageId, itemId int }
	byKey := make(map[key]*model.TotalQuantityResultForTrend, len(a))
	for _, result := range a {
		byKey[key{result.GroupID, result.StageID, result.ItemID}] = result
	}
	for _, result := range b {
		k := key{result.GroupID, result.StageID, result.ItemID}
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

// addTrendTimesResults adds the times of b to those of the same bucket and stage in a, and appends the others of b.
func addTrendTimesResults(a, b []*model.TotalTimesResultForTrend) []*model.TotalTimesResultForTrend {
	type key struct{ groupId, stageId int }
	byKey := make(map[key]*model.TotalTimesResultForTrend, len(a))
	for _, result := range a {
		byKey[key{result.GroupID, result.StageID}] = result
	}
	for _, result := range b {
		k := key{result.GroupID, result.StageID}
		if sum, ok := byKey[k]; ok {
			sum.TotalTimes += result.TotalTimes
			continue
		}
		sum := *result
		byKey[k] = &sum
		a = append(a, &sum)
	}
	return a
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
