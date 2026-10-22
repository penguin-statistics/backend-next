package service

// The calculations of v3.14.18, before the worker performance work, kept verbatim but for their caches as
// references for the integration tests. Its GetAllMaxAccumulableTimeRangesByServer is gone: #794 changed how the
// time ranges of the global drop matrix accumulate, so the references calculate over the current ones.

import (
	"context"
	"sort"
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/samber/lo"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/util"
)

func legacyGetMaxAccumulableTimeRangesByServer(ctx context.Context, s *TimeRange, server string) (map[int]map[int][]*model.TimeRange, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	timeRangesMap, err := s.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}
	maxAccumulableTimeRanges := make(map[int]map[int][]*model.TimeRange)
	for _, el := range util.GroupBy(legacyDropInfosWithItem(dropInfos), func(dropInfo *model.DropInfo) int { return dropInfo.StageID }) {
		stageId := el.Key
		maxAccumulableTimeRangesForOneStage := make(map[int][]*model.TimeRange)
		for _, el := range util.GroupBy(el.Items, func(dropInfo *model.DropInfo) int { return int(dropInfo.ItemID.Int64) }) {
			itemId := el.Key
			sortedDropInfos := legacyDistinctBy(el.Items, func(dropInfo *model.DropInfo) int { return dropInfo.RangeID })
			legacySort(sortedDropInfos, func(a, b *model.DropInfo) bool {
				return timeRangesMap[a.RangeID].StartTime.After(*timeRangesMap[b.RangeID].StartTime)
			})
			startIdx := len(sortedDropInfos) - 1
			endIdx := 0
			timeRanges := make([]*model.TimeRange, 0)
			for idx, dropInfo := range sortedDropInfos {
				if !dropInfo.Accumulable {
					startIdx = idx
					if idx != 0 {
						startIdx = idx - 1
					}
					break
				}
			}
			for i := endIdx; i <= startIdx; i++ {
				timeRanges = append(timeRanges, timeRangesMap[sortedDropInfos[i].RangeID])
			}
			if len(timeRanges) > 0 {
				maxAccumulableTimeRangesForOneStage[itemId] = timeRanges
			}
		}
		if len(maxAccumulableTimeRangesForOneStage) > 0 {
			maxAccumulableTimeRanges[stageId] = maxAccumulableTimeRangesForOneStage
		}
	}
	return maxAccumulableTimeRanges, nil
}

func legacyGetLatestTimeRangesByServer(ctx context.Context, s *TimeRange, server string) (map[int]*model.TimeRange, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	timeRangesMap, err := s.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}
	results := make(map[int]*model.TimeRange)
	for _, el := range util.GroupBy(legacyDropInfosWithItem(dropInfos), func(dropInfo *model.DropInfo) int { return dropInfo.StageID }) {
		stageId := el.Key
		sortedDropInfos := legacyDistinctBy(el.Items, func(dropInfo *model.DropInfo) *model.DropInfo { return dropInfo })
		legacySort(sortedDropInfos, func(a, b *model.DropInfo) bool {
			return timeRangesMap[a.RangeID].StartTime.After(*timeRangesMap[b.RangeID].StartTime)
		})
		results[stageId] = timeRangesMap[sortedDropInfos[0].RangeID]
	}
	return results, nil
}

func legacyCalcDropMatrixByGivenDate(
	ctx context.Context, s *DropMatrix, server string, date *time.Time, endTime *time.Time, sourceCategories []string,
) ([]*model.DropMatrixElement, error) {
	dropMatrixElements := make([]*model.DropMatrixElement, 0)

	start := time.UnixMilli(util.GetDayStartTime(date, server))
	startNextDay := start.Add(time.Hour * 24)
	end := lo.Ternary(endTime == nil, &startNextDay, endTime)

	timeRangeGiven := &model.TimeRange{
		StartTime: &start,
		EndTime:   end,
	}

	// the segments of #794 (option B) replaced those of v3.14.18: the reference renders the days over the current ones,
	// as legacyCalcGlobalDropMatrix does
	timeRangesMap, err := s.TimeRangeService.GetAllMaxAccumulableTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	stageIdsItemIdsMapByTimeRangeStr := make(map[string]map[int][]int, 0)
	for stageId, timeRangesMapByItemId := range timeRangesMap {
		for itemId, timeRanges := range timeRangesMapByItemId {
			for _, timeRange := range timeRanges {
				intersection := util.GetIntersection(timeRange, timeRangeGiven)
				if intersection == nil {
					continue
				}
				intersectionStr := intersection.String()
				if _, ok := stageIdsItemIdsMapByTimeRangeStr[intersectionStr]; !ok {
					stageIdsItemIdsMapByTimeRangeStr[intersectionStr] = make(map[int][]int, 0)
				}
				if _, ok := stageIdsItemIdsMapByTimeRangeStr[intersectionStr][stageId]; !ok {
					stageIdsItemIdsMapByTimeRangeStr[intersectionStr][stageId] = make([]int, 0)
				}
				stageIdsItemIdsMapByTimeRangeStr[intersectionStr][stageId] = append(stageIdsItemIdsMapByTimeRangeStr[intersectionStr][stageId], itemId)
			}
		}
	}

	for timeRangeStr, stageIdsItemIdsMap := range stageIdsItemIdsMapByTimeRangeStr {
		timeRange := model.TimeRangeFromString(timeRangeStr)
		for _, sourceCategory := range sourceCategories {
			queryCtx := &model.DropReportQueryContext{
				Server:             server,
				StartTime:          timeRange.StartTime,
				EndTime:            timeRange.EndTime,
				SourceCategory:     sourceCategory,
				ExcludeNonOneTimes: false,
				StageItemFilter:    &stageIdsItemIdsMap,
			}
			res, err := legacyCalcDropMatrix(ctx, s, queryCtx)
			if err != nil {
				return nil, err
			}
			dropMatrixElements = append(dropMatrixElements, res...)
		}
	}
	return dropMatrixElements, nil
}

func legacyCalcDropMatrix(ctx context.Context, s *DropMatrix, queryCtx *model.DropReportQueryContext) ([]*model.DropMatrixElement, error) {
	var combinedResults []*model.CombinedResultForDropMatrix
	timesResults, err := s.DropReportService.CalcTotalTimesForDropMatrix(ctx, queryCtx)
	if err != nil {
		return nil, err
	}
	quantityUniqCountResults, err := s.DropReportService.CalcQuantityUniqCount(ctx, queryCtx)
	if err != nil {
		return nil, err
	}
	quantityResults := calcTotalQuantityForDropMatrix(quantityUniqCountResults)

	oneBatch := legacyCombineDropMatrixQuantityAndTimesResults(quantityResults, timesResults, quantityUniqCountResults)
	combinedResults = append(combinedResults, oneBatch...)

	// save stage times for later use
	stageTimesMap := map[int]int{}

	// grouping results by stage id
	dropMatrixElements := make([]*model.DropMatrixElement, 0)
	for _, el := range util.GroupBy(combinedResults, func(el *model.CombinedResultForDropMatrix) int { return el.StageID }) {
		stageId := el.Key
		itemIds := (*queryCtx.StageItemFilter)[stageId]

		// get all item ids which are dropped in this stage, save in dropSet
		timeRange := &model.TimeRange{
			StartTime: queryCtx.StartTime,
			EndTime:   queryCtx.EndTime,
		}
		dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(
			ctx, queryCtx.Server, []*model.TimeRange{timeRange}, []int{stageId}, itemIds)
		if err != nil {
			return nil, err
		}
		// the item ids of the drop infos that are in itemIds (none for no itemIds)
		var dropItemIds []int
		for _, dropInfo := range legacyDropInfosWithItem(dropInfos) {
			if itemId := int(dropInfo.ItemID.Int64); legacyContains(itemIds, itemId) {
				dropItemIds = append(dropItemIds, itemId)
			}
		}
		// use a fake hashset to save item ids
		dropSet := make(map[int]struct{}, len(dropItemIds))
		for _, itemId := range dropItemIds {
			dropSet[itemId] = struct{}{}
		}

		for _, el2 := range el.Items {
			itemId := el2.ItemID
			quantity := el2.Quantity
			times := el2.Times
			quantityBuckets := el2.QuantityBuckets
			dropMatrixElement := model.DropMatrixElement{
				StageID:         stageId,
				ItemID:          itemId,
				Quantity:        quantity,
				QuantityBuckets: quantityBuckets,
				Times:           times,
				Server:          queryCtx.Server,
				SourceCategory:  queryCtx.SourceCategory,
				StartTime:       queryCtx.StartTime,
				EndTime:         queryCtx.EndTime,
				DayNum:          util.GetDayNum(queryCtx.StartTime, queryCtx.Server),
			}
			dropMatrixElements = append(dropMatrixElements, &dropMatrixElement)
			delete(dropSet, itemId)        // remove existing item ids from drop set
			stageTimesMap[stageId] = times // record stage times into a map
		}
		// add those items which do not show up in the matrix (quantity is 0)
		for itemId := range dropSet {
			times := stageTimesMap[stageId]
			dropMatrixElementWithZeroQuantity := model.DropMatrixElement{
				StageID:         stageId,
				ItemID:          itemId,
				Quantity:        0,
				QuantityBuckets: map[int]int{0: times},
				Times:           times,
				Server:          queryCtx.Server,
				SourceCategory:  queryCtx.SourceCategory,
				StartTime:       queryCtx.StartTime,
				EndTime:         queryCtx.EndTime,
				DayNum:          util.GetDayNum(queryCtx.StartTime, queryCtx.Server),
			}
			dropMatrixElements = append(dropMatrixElements, &dropMatrixElementWithZeroQuantity)
		}
	}
	return dropMatrixElements, nil
}

func legacyCalcPatternMatrixForTimeRanges(
	ctx context.Context, s *PatternMatrix, server string, timeRanges []*model.TimeRange, stageIdFilter []int, accountId null.Int, sourceCategory string,
) ([]*model.PatternMatrixElement, error) {
	results := make([]*model.PatternMatrixElement, 0)

	dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, timeRanges, stageIdFilter, nil)
	if err != nil {
		return nil, err
	}

	stageIds := util.GetStageIdsFromDropInfos(dropInfos)
	stageItemFilter := make(map[int][]int, 0)
	for _, stageId := range stageIds {
		stageItemFilter[stageId] = make([]int, 0)
	}
	for _, timeRange := range timeRanges {
		queryCtx := &model.DropReportQueryContext{
			Server:             server,
			StartTime:          timeRange.StartTime,
			EndTime:            timeRange.EndTime,
			AccountID:          accountId,
			StageItemFilter:    &stageItemFilter,
			SourceCategory:     sourceCategory,
			ExcludeNonOneTimes: true,
		}
		quantityResults, err := s.DropReportService.CalcTotalQuantityForPatternMatrix(ctx, queryCtx)
		if err != nil {
			return nil, err
		}
		timesResults, err := s.DropReportService.CalcTotalTimesForPatternMatrix(ctx, queryCtx)
		if err != nil {
			return nil, err
		}
		combinedResults := legacyCombinePatternQuantityAndTimesResults(quantityResults, timesResults)
		for _, result := range combinedResults {
			results = append(results, &model.PatternMatrixElement{
				StageID:        result.StageID,
				PatternID:      result.PatternID,
				StartTime:      queryCtx.StartTime,
				EndTime:        queryCtx.EndTime,
				DayNum:         util.GetDayNum(queryCtx.StartTime, queryCtx.Server),
				Quantity:       result.Quantity,
				Times:          result.Times,
				Server:         server,
				SourceCategory: sourceCategory,
				RangeID:        timeRange.RangeID,
			})
		}
	}
	return results, nil
}

// the v3.14.18 drop matrix of time ranges (personal and customized): two queries per time range
func legacyCalcDropMatrixForTimeRanges(
	ctx context.Context, s *DropMatrix, server string, timeRanges []*model.TimeRange, stageIdFilter []int, itemIdFilter []int, accountId null.Int, sourceCategory string,
) ([]*model.DropMatrixElement, error) {
	dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, timeRanges, stageIdFilter, itemIdFilter)
	if err != nil {
		return nil, err
	}

	var combinedResults []*model.CombinedResultForDropMatrix
	for _, timeRange := range timeRanges {
		stageItemFilter := util.GetStageIdItemIdMapFromDropInfos(dropInfos)
		queryCtx := &model.DropReportQueryContext{
			Server:             server,
			StartTime:          timeRange.StartTime,
			EndTime:            timeRange.EndTime,
			AccountID:          accountId,
			StageItemFilter:    &stageItemFilter,
			SourceCategory:     sourceCategory,
			ExcludeNonOneTimes: false,
		}
		timesResults, err := s.DropReportService.CalcTotalTimesForDropMatrix(ctx, queryCtx)
		if err != nil {
			return nil, err
		}
		quantityUniqCountResults, err := s.DropReportService.CalcQuantityUniqCount(ctx, queryCtx)
		if err != nil {
			return nil, err
		}
		quantityResults := calcTotalQuantityForDropMatrix(quantityUniqCountResults)
		oneBatch := legacyCombineDropMatrixQuantityAndTimesResults(quantityResults, timesResults, quantityUniqCountResults)
		for _, result := range oneBatch {
			result.TimeRange = timeRange
		}
		combinedResults = append(combinedResults, oneBatch...)
	}

	// save stage times for later use
	stageTimesMap := map[int]int{}

	// grouping results by stage id
	dropMatrixElements := make([]*model.DropMatrixElement, 0)
	for _, el := range util.GroupBy(combinedResults, func(el *model.CombinedResultForDropMatrix) int { return el.StageID }) {
		stageId := el.Key
		for _, el2 := range util.GroupBy(el.Items, func(el *model.CombinedResultForDropMatrix) int { return el.TimeRange.RangeID }) {
			rangeId := el2.Key
			timeRange := el2.Items[0].TimeRange

			// get all item ids which are dropped in this stage and in this time range
			var dropItemIds []int
			if rangeId == 0 {
				// rangeId == 0 means it is a customized time range instead of a time range from the database
				dropInfosForSpecialTimeRange, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, []*model.TimeRange{el2.Items[0].TimeRange}, []int{stageId}, itemIdFilter)
				if err != nil {
					return nil, err
				}
				for _, dropInfo := range legacyDropInfosWithItem(dropInfosForSpecialTimeRange) {
					dropItemIds = append(dropItemIds, int(dropInfo.ItemID.Int64))
				}
			} else {
				dropItemIds, _ = s.DropInfoService.GetItemDropSetByStageIdAndRangeId(ctx, server, stageId, rangeId)
			}

			// if item id filter is applied, then filter the drop item ids
			if len(itemIdFilter) > 0 {
				var filtered []int
				for _, itemId := range dropItemIds {
					if legacyContains(itemIdFilter, itemId) {
						filtered = append(filtered, itemId)
					}
				}
				dropItemIds = filtered
			}

			// use a fake hashset to save item ids
			dropSet := make(map[int]struct{}, len(dropItemIds))
			for _, itemId := range dropItemIds {
				dropSet[itemId] = struct{}{}
			}

			for _, el3 := range el2.Items {
				itemId := el3.ItemID
				quantity := el3.Quantity
				times := el3.Times
				quantityBuckets := el3.QuantityBuckets
				dropMatrixElement := model.DropMatrixElement{
					StageID:         stageId,
					ItemID:          itemId,
					RangeID:         rangeId,
					Quantity:        quantity,
					QuantityBuckets: quantityBuckets,
					Times:           times,
					Server:          server,
					SourceCategory:  sourceCategory,
				}
				if rangeId == 0 {
					dropMatrixElement.TimeRange = timeRange
				}
				dropMatrixElements = append(dropMatrixElements, &dropMatrixElement)
				delete(dropSet, itemId)        // remove existing item ids from drop set
				stageTimesMap[stageId] = times // record stage times into a map
			}
			// add those items which do not show up in the matrix (quantity is 0)
			for itemId := range dropSet {
				times := stageTimesMap[stageId]
				dropMatrixElementWithZeroQuantity := model.DropMatrixElement{
					StageID:         stageId,
					ItemID:          itemId,
					RangeID:         rangeId,
					Quantity:        0,
					QuantityBuckets: map[int]int{0: times},
					Times:           times,
					Server:          server,
					SourceCategory:  sourceCategory,
				}
				if rangeId == 0 {
					dropMatrixElementWithZeroQuantity.TimeRange = timeRange
				}
				dropMatrixElements = append(dropMatrixElements, &dropMatrixElementWithZeroQuantity)
			}
		}
	}
	return dropMatrixElements, nil
}

func legacyCombinePatternQuantityAndTimesResults(
	quantityResults []*model.TotalQuantityResultForPatternMatrix, timesResults []*model.TotalTimesResult,
) []*model.CombinedResultForDropPattern {
	combinedResults := make([]*model.CombinedResultForDropPattern, 0)
	// stage id -> pattern id -> quantity, the last one of the same ids kept
	quantityResultsMap := make(map[int]map[int]int)
	for _, result := range quantityResults {
		if quantityResultsMap[result.StageID] == nil {
			quantityResultsMap[result.StageID] = make(map[int]int)
		}
		quantityResultsMap[result.StageID][result.PatternID] = result.TotalQuantity
	}

	for _, secondGroupResults := range util.GroupBy(timesResults, func(result *model.TotalTimesResult) int { return result.StageID }) {
		stageId := secondGroupResults.Key
		quantityResultsMapForOneStage := quantityResultsMap[stageId]
		for _, el := range secondGroupResults.Items {
			times := el.TotalTimes
			for patternId, quantity := range quantityResultsMapForOneStage {
				combinedResults = append(combinedResults, &model.CombinedResultForDropPattern{
					StageID:   stageId,
					PatternID: patternId,
					Quantity:  quantity,
					Times:     times,
				})
			}
		}
	}
	return combinedResults
}

// the v3.14.18 combineQuantityAndTimesResults of the drop matrix, without its validation warning and time range
func legacyCombineDropMatrixQuantityAndTimesResults(
	quantityResults []*model.TotalQuantityResultForDropMatrix, timesResults []*model.TotalTimesResult,
	quantityUniqCountResults []*model.QuantityUniqCountResultForDropMatrix,
) []*model.CombinedResultForDropMatrix {
	combinedResults := make([]*model.CombinedResultForDropMatrix, 0)

	// stage id -> item id -> quantity, the last one of the same ids kept
	quantityResultsMap := make(map[int]map[int]int)
	for _, result := range quantityResults {
		if quantityResultsMap[result.StageID] == nil {
			quantityResultsMap[result.StageID] = make(map[int]int)
		}
		quantityResultsMap[result.StageID][result.ItemID] = result.TotalQuantity
	}

	// stage id -> item id -> quantity -> count, the last one of the same ids kept
	quantityUniqCountResultsMap := make(map[int]map[int]map[int]int)
	for _, result := range quantityUniqCountResults {
		if quantityUniqCountResultsMap[result.StageID] == nil {
			quantityUniqCountResultsMap[result.StageID] = make(map[int]map[int]int)
		}
		if quantityUniqCountResultsMap[result.StageID][result.ItemID] == nil {
			quantityUniqCountResultsMap[result.StageID][result.ItemID] = make(map[int]int)
		}
		quantityUniqCountResultsMap[result.StageID][result.ItemID][result.Quantity] = result.Count
	}

	for _, secondGroupResults := range util.GroupBy(timesResults, func(result *model.TotalTimesResult) int { return result.StageID }) {
		stageId := secondGroupResults.Key
		quantityResultsMapForOneStage := quantityResultsMap[stageId]
		quantityUniqCountResultsMapForOneStage := quantityUniqCountResultsMap[stageId]
		for _, el := range secondGroupResults.Items {
			times := el.TotalTimes
			for itemId, quantity := range quantityResultsMapForOneStage {
				combinedResults = append(combinedResults, &model.CombinedResultForDropMatrix{
					StageID:         stageId,
					ItemID:          itemId,
					Quantity:        quantity,
					QuantityBuckets: quantityUniqCountResultsMapForOneStage[itemId],
					Times:           times,
				})
			}
		}
	}
	return combinedResults
}

// the v3.14.18 global drop matrix: three queries per latest max accumulable time range (repo GetAll*ForGlobalDropMatrix)
func legacyCalcGlobalDropMatrix(ctx context.Context, db *bun.DB, s *DropMatrix, server string, sourceCategory string) (*model.DropMatrixQueryResult, error) {
	finalResult := &model.DropMatrixQueryResult{
		Matrix: make([]*model.OneDropMatrixElement, 0),
	}
	maxAccumulableTimeRanges, err := s.TimeRangeService.GetAllMaxAccumulableTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	latestMaxAccumulableTimeRanges := make(map[int]map[int]*model.TimeRange, 0)
	for stageId, timeRangesMapByItemId := range maxAccumulableTimeRanges {
		if _, ok := latestMaxAccumulableTimeRanges[stageId]; !ok {
			latestMaxAccumulableTimeRanges[stageId] = make(map[int]*model.TimeRange, 0)
		}
		for itemId, timeRanges := range timeRangesMapByItemId {
			latestMaxAccumulableTimeRanges[stageId][itemId] = timeRanges[len(timeRanges)-1]
		}
	}
	stageIdsItemIdsMapByTimeRangeStr := make(map[string]map[int][]int, 0)
	for stageId, timeRangeMapByItemId := range latestMaxAccumulableTimeRanges {
		for itemId, timeRange := range timeRangeMapByItemId {
			timeRangeStr := timeRange.String()
			if _, ok := stageIdsItemIdsMapByTimeRangeStr[timeRangeStr]; !ok {
				stageIdsItemIdsMapByTimeRangeStr[timeRangeStr] = make(map[int][]int, 0)
			}
			stageIdsItemIdsMapByTimeRangeStr[timeRangeStr][stageId] = append(stageIdsItemIdsMapByTimeRangeStr[timeRangeStr][stageId], itemId)
		}
	}
	for timeRangeStr, stageIdsItemIdsMap := range stageIdsItemIdsMapByTimeRangeStr {
		timeRange := model.TimeRangeFromString(timeRangeStr)
		stageIds := make([]int, 0)
		for stageId := range stageIdsItemIdsMap {
			stageIds = append(stageIds, stageId)
		}
		elements := func() *bun.SelectQuery {
			return db.NewSelect().
				TableExpr("drop_matrix_elements").
				Where("server = ?", server).
				Where("source_category = ?", sourceCategory).
				Where("stage_id IN (?)", bun.In(stageIds)).
				Where("start_time >= timestamp with time zone ?", timeRange.StartTime.Format(time.RFC3339)).
				Where("end_time <= timestamp with time zone ?", timeRange.EndTime.Format(time.RFC3339))
		}

		var timesResults []*legacyGlobalDropMatrixTimes
		if err := db.NewSelect().
			TableExpr("(?) AS subq1", db.NewSelect().
				TableExpr("(?) AS subq2", elements().Column("stage_id", "item_id", "times", "day_num").Where("times > 0")).
				Column("stage_id", "item_id", "times").
				Group("stage_id", "item_id", "times", "day_num")).
			Column("stage_id", "item_id").
			ColumnExpr("SUM(times) AS times").
			Group("stage_id", "item_id").
			Scan(ctx, &timesResults); err != nil {
			return nil, err
		}
		var quantityResults []*legacyGlobalDropMatrixQuantity
		if err := db.NewSelect().
			TableExpr("(?) AS subq1", elements().Column("stage_id", "item_id", "quantity").Where("quantity > 0")).
			Column("stage_id", "item_id").
			ColumnExpr("SUM(quantity) AS quantity").
			Group("stage_id", "item_id").
			Scan(ctx, &quantityResults); err != nil {
			return nil, err
		}
		var bucketResults []*legacyGlobalDropMatrixBuckets
		if err := db.NewSelect().
			TableExpr("(?) AS subq1", db.NewSelect().
				TableExpr("(?) AS subq2", elements().Column("stage_id", "item_id", "quantity_buckets").Where("quantity > 0")).
				TableExpr("jsonb_each_text(quantity_buckets)").
				Column("stage_id", "item_id", "key").
				ColumnExpr("sum(value::numeric) val").
				Group("stage_id", "item_id", "key")).
			Column("stage_id", "item_id").
			ColumnExpr("json_object_agg(key, val) AS quantity_buckets").
			Group("stage_id", "item_id").
			Scan(ctx, &bucketResults); err != nil {
			return nil, err
		}
		times := lo.SliceToMap(timesResults, func(r *legacyGlobalDropMatrixTimes) ([2]int, int) {
			return [2]int{r.StageID, r.ItemID}, r.Times
		})
		quantities := lo.SliceToMap(quantityResults, func(r *legacyGlobalDropMatrixQuantity) ([2]int, int) {
			return [2]int{r.StageID, r.ItemID}, r.Quantity
		})
		buckets := lo.SliceToMap(bucketResults, func(r *legacyGlobalDropMatrixBuckets) ([2]int, map[int]int) {
			return [2]int{r.StageID, r.ItemID}, r.QuantityBuckets
		})

		for stageId, itemIds := range stageIdsItemIdsMap {
			for _, itemId := range itemIds {
				k := [2]int{stageId, itemId}
				timesResult, foundTimes := times[k]
				quantityResult, foundQuantity := quantities[k]
				bucketsResult, foundBuckets := buckets[k]
				if !foundTimes || !foundQuantity || !foundBuckets {
					continue
				}
				finalResult.Matrix = append(finalResult.Matrix, &model.OneDropMatrixElement{
					StageID:   stageId,
					ItemID:    itemId,
					Times:     timesResult,
					Quantity:  quantityResult,
					TimeRange: timeRange,
					StdDev:    util.RoundFloat64(util.CalcStdDevFromQuantityBuckets(bucketsResult, timesResult, false), constant.StdDevDigits),
				})
			}
		}
	}
	return finalResult, nil
}

// the v3.14.18 global pattern matrix: two queries per latest time range (repo GetAll*ForGlobalPatternMatrix)
func legacyCalcGlobalPatternMatrix(ctx context.Context, db *bun.DB, s *PatternMatrix, server string, sourceCategory string) (*model.PatternMatrixQueryResult, error) {
	finalResult := &model.PatternMatrixQueryResult{
		PatternMatrix: make([]*model.OnePatternMatrixElement, 0),
	}
	latestTimeRanges, err := s.TimeRangeService.GetLatestTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	stageIdsMapByTimeRangeStr := make(map[string][]int, 0)
	for stageId, timeRange := range latestTimeRanges {
		stageIdsMapByTimeRangeStr[timeRange.String()] = append(stageIdsMapByTimeRangeStr[timeRange.String()], stageId)
	}
	for timeRangeStr, stageIds := range stageIdsMapByTimeRangeStr {
		timeRange := model.TimeRangeFromString(timeRangeStr)
		elements := func() *bun.SelectQuery {
			return db.NewSelect().
				TableExpr("pattern_matrix_elements").
				Where("server = ?", server).
				Where("source_category = ?", sourceCategory).
				Where("stage_id IN (?)", bun.In(stageIds)).
				Where("start_time >= timestamp with time zone ?", timeRange.StartTime.Format(time.RFC3339)).
				Where("end_time <= timestamp with time zone ?", timeRange.EndTime.Format(time.RFC3339))
		}
		var timesResults []*legacyGlobalPatternMatrixTimes
		if err := db.NewSelect().
			TableExpr("(?) AS subq1", db.NewSelect().
				TableExpr("(?) AS subq2", elements().Column("stage_id", "times", "day_num").Where("times > 0")).
				Column("stage_id").
				ColumnExpr("MIN(times) AS times").
				Group("stage_id", "day_num")).
			Column("stage_id").
			ColumnExpr("SUM(times) AS times").
			Group("stage_id").
			Scan(ctx, &timesResults); err != nil {
			return nil, err
		}
		var quantityResults []*legacyGlobalPatternMatrixQuantity
		if err := db.NewSelect().
			TableExpr("(?) AS subq1", elements().Column("stage_id", "pattern_id", "quantity").Where("quantity > 0")).
			Column("stage_id", "pattern_id").
			ColumnExpr("SUM(quantity) AS quantity").
			Group("stage_id", "pattern_id").
			Scan(ctx, &quantityResults); err != nil {
			return nil, err
		}
		times := lo.SliceToMap(timesResults, func(r *legacyGlobalPatternMatrixTimes) (int, int) { return r.StageID, r.Times })
		for _, quantityResult := range quantityResults {
			finalResult.PatternMatrix = append(finalResult.PatternMatrix, &model.OnePatternMatrixElement{
				StageID:   quantityResult.StageID,
				PatternID: quantityResult.PatternID,
				TimeRange: timeRange,
				Times:     times[quantityResult.StageID],
				Quantity:  quantityResult.Quantity,
			})
		}
	}
	return finalResult, nil
}

// the v3.14.18 results of the global matrix queries
type legacyGlobalDropMatrixTimes struct {
	StageID int `bun:"stage_id"`
	ItemID  int `bun:"item_id"`
	Times   int `bun:"times"`
}

type legacyGlobalDropMatrixQuantity struct {
	StageID  int `bun:"stage_id"`
	ItemID   int `bun:"item_id"`
	Quantity int `bun:"quantity"`
}

type legacyGlobalDropMatrixBuckets struct {
	StageID         int         `bun:"stage_id"`
	ItemID          int         `bun:"item_id"`
	QuantityBuckets map[int]int `bun:"type:jsonb"`
}

type legacyGlobalPatternMatrixTimes struct {
	StageID int `bun:"stage_id"`
	Times   int `bun:"times"`
}

type legacyGlobalPatternMatrixQuantity struct {
	StageID   int `bun:"stage_id"`
	PatternID int `bun:"pattern_id"`
	Quantity  int `bun:"quantity"`
}

// DropInfo.GetCurrentDropInfosByServer of v3.14.18: every drop info of server, filtered by the current time ranges
func legacyGetCurrentDropInfosByServer(ctx context.Context, s *DropInfo, server string) ([]*model.DropInfo, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	currentTimeRanges, err := s.TimeRangeService.GetCurrentTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	currentTimeRangesMap := make(map[int]*model.TimeRange)
	for _, timeRange := range currentTimeRanges {
		currentTimeRangesMap[timeRange.RangeID] = timeRange
	}
	currentDropInfos := dropInfos[:0]
	for _, dropInfo := range dropInfos {
		if currentTimeRangesMap[dropInfo.RangeID] != nil {
			currentDropInfos = append(currentDropInfos, dropInfo)
		}
	}
	return currentDropInfos, nil
}

// the shim of v3.14.18: loads the current drop infos of server for the opening stages on every call
func legacyApplyShimForDropMatrixQuery(ctx context.Context, s *DropMatrix, server string, showClosedZones bool, stageFilterStr, itemFilterStr string, queryResult *model.DropMatrixQueryResult) (*modelv2.DropMatrixQueryResult, error) {
	itemsMapById, err := s.ItemService.GetItemsMapById(ctx)
	if err != nil {
		return nil, err
	}

	stagesMapById, err := s.StageService.GetStagesMapById(ctx)
	if err != nil {
		return nil, err
	}

	// get opening stages from dropinfos
	var openingStageIds []int
	if !showClosedZones {
		currentDropInfos, err := legacyGetCurrentDropInfosByServer(ctx, s.DropInfoService, server)
		if err != nil {
			return nil, err
		}
		for _, dropInfo := range legacyDistinctBy(currentDropInfos, func(dropInfo *model.DropInfo) int { return dropInfo.StageID }) {
			openingStageIds = append(openingStageIds, dropInfo.StageID)
		}
	}

	// convert comma-splitted stage filter param to a hashset
	stageFilter := make([]string, 0)
	if stageFilterStr != "" {
		stageFilter = strings.Split(stageFilterStr, ",")
	}
	stageFilterSet := make(map[string]struct{}, len(stageFilter))
	for _, stageIdStr := range stageFilter {
		stageFilterSet[stageIdStr] = struct{}{}
	}

	// convert comma-splitted item filter param to a hashset
	itemFilter := make([]string, 0)
	if itemFilterStr != "" {
		itemFilter = strings.Split(itemFilterStr, ",")
	}
	itemFilterSet := make(map[string]struct{}, len(itemFilter))
	for _, itemIdStr := range itemFilter {
		itemFilterSet[itemIdStr] = struct{}{}
	}

	results := &modelv2.DropMatrixQueryResult{
		Matrix: make([]*modelv2.OneDropMatrixElement, 0),
	}
	for _, el := range queryResult.Matrix {
		if !showClosedZones && !legacyContains(openingStageIds, el.StageID) {
			continue
		}

		stage := stagesMapById[el.StageID]
		if len(stageFilterSet) > 0 {
			if _, ok := stageFilterSet[stage.ArkStageID]; !ok {
				continue
			}
		}

		item := itemsMapById[el.ItemID]
		if len(itemFilterSet) > 0 {
			if _, ok := itemFilterSet[item.ArkItemID]; !ok {
				continue
			}
		}

		// if end time is after now, set it to be null, so that the frontend will show it as "till now"
		var endTime null.Int
		if el.TimeRange.EndTime.After(time.Now()) {
			endTime = null.NewInt(0, false)
		} else {
			endTime = null.NewInt(el.TimeRange.EndTime.UnixMilli(), true)
		}
		oneDropMatrixElement := modelv2.OneDropMatrixElement{
			StageID:   stage.ArkStageID,
			ItemID:    item.ArkItemID,
			Quantity:  el.Quantity,
			Times:     el.Times,
			StdDev:    el.StdDev,
			StartTime: el.TimeRange.StartTime.UnixMilli(),
			EndTime:   endTime,
		}
		results.Matrix = append(results.Matrix, &oneDropMatrixElement)
	}
	return results, nil
}

// The helpers of the references above, in place of the go-linq calls of v3.14.18: group members keep their input
// order (util.GroupBy), and sorting is sort.Sort, as go-linq's.

func legacyDropInfosWithItem(dropInfos []*model.DropInfo) []*model.DropInfo {
	var results []*model.DropInfo
	for _, dropInfo := range dropInfos {
		if dropInfo.ItemID.Valid {
			results = append(results, dropInfo)
		}
	}
	return results
}

// legacyDistinctBy keeps the first of the items with the same key.
func legacyDistinctBy[T any, K comparable](items []T, key func(T) K) []T {
	seen := make(map[K]struct{})
	var results []T
	for _, item := range items {
		if _, ok := seen[key(item)]; !ok {
			seen[key(item)] = struct{}{}
			results = append(results, item)
		}
	}
	return results
}

type legacySorter[T any] struct {
	items []T
	less  func(a, b T) bool
}

func (s legacySorter[T]) Len() int           { return len(s.items) }
func (s legacySorter[T]) Swap(i, j int)      { s.items[i], s.items[j] = s.items[j], s.items[i] }
func (s legacySorter[T]) Less(i, j int) bool { return s.less(s.items[i], s.items[j]) }

func legacySort[T any](items []T, less func(a, b T) bool) {
	sort.Sort(legacySorter[T]{items: items, less: less})
}

func legacyContains(items []int, item int) bool {
	for _, i := range items {
		if i == item {
			return true
		}
	}
	return false
}
