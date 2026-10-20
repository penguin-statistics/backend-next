package service

// The calculations of v3.14.18, before the worker performance work, kept verbatim but for their caches as
// references for the integration tests.

import (
	"context"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/ahmetb/go-linq/v3"
	"github.com/samber/lo"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
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
	var groupedResults []linq.Group
	linq.From(dropInfos).
		WhereT(func(dropInfo *model.DropInfo) bool { return dropInfo.ItemID.Valid }).
		GroupByT(
			func(dropInfo *model.DropInfo) int { return dropInfo.StageID },
			func(dropInfo *model.DropInfo) *model.DropInfo { return dropInfo },
		).
		ToSlice(&groupedResults)
	for _, el := range groupedResults {
		stageId := el.Key.(int)
		var groupedResults2 []linq.Group
		linq.From(el.Group).
			GroupByT(
				func(dropInfo any) int { return int(dropInfo.(*model.DropInfo).ItemID.Int64) },
				func(dropInfo any) *model.DropInfo { return dropInfo.(*model.DropInfo) },
			).
			ToSlice(&groupedResults2)
		maxAccumulableTimeRangesForOneStage := make(map[int][]*model.TimeRange)
		for _, el := range groupedResults2 {
			itemId := el.Key.(int)
			var sortedDropInfos []*model.DropInfo
			linq.From(el.Group).
				DistinctByT(
					func(dropInfo *model.DropInfo) int { return dropInfo.RangeID },
				).
				SortT(
					func(a, b *model.DropInfo) bool {
						return timeRangesMap[a.RangeID].StartTime.After(*timeRangesMap[b.RangeID].StartTime)
					}).
				ToSlice(&sortedDropInfos)
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

func legacyGetAllMaxAccumulableTimeRangesByServer(ctx context.Context, s *TimeRange, server string) (map[int]map[int][]*model.TimeRange, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	timeRangesMap, err := s.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}
	maxAccumulableTimeRanges := make(map[int]map[int][]*model.TimeRange)
	var groupedResults []linq.Group
	linq.From(dropInfos).
		WhereT(func(dropInfo *model.DropInfo) bool { return dropInfo.ItemID.Valid }).
		GroupByT(
			func(dropInfo *model.DropInfo) int { return dropInfo.StageID },
			func(dropInfo *model.DropInfo) *model.DropInfo { return dropInfo },
		).
		ToSlice(&groupedResults)
	for _, el := range groupedResults {
		stageId := el.Key.(int)
		var groupedResults2 []linq.Group
		linq.From(el.Group).
			GroupByT(
				func(dropInfo any) int { return int(dropInfo.(*model.DropInfo).ItemID.Int64) },
				func(dropInfo any) *model.DropInfo { return dropInfo.(*model.DropInfo) },
			).
			ToSlice(&groupedResults2)
		maxAccumulableTimeRangesForOneStage := make(map[int][]*model.TimeRange)
		for _, el := range groupedResults2 {
			itemId := el.Key.(int)
			var sortedDropInfos []*model.DropInfo
			linq.From(el.Group).
				DistinctByT(
					func(dropInfo *model.DropInfo) int { return dropInfo.RangeID },
				).
				SortT(
					func(a, b *model.DropInfo) bool {
						return timeRangesMap[a.RangeID].StartTime.Before(*timeRangesMap[b.RangeID].StartTime)
					}).
				ToSlice(&sortedDropInfos)

			timeRanges := make([]*model.TimeRange, 0)
			for _, dropInfo := range sortedDropInfos {
				timeRange := timeRangesMap[dropInfo.RangeID]
				if len(timeRanges) == 0 || !dropInfo.Accumulable {
					pureRange := &model.TimeRange{
						StartTime: timeRange.StartTime,
						EndTime:   timeRange.EndTime,
					}
					timeRanges = append(timeRanges, pureRange)
					continue
				}
				lastAddedTimeRange := timeRanges[len(timeRanges)-1]
				if !lastAddedTimeRange.EndTime.Before(*timeRange.StartTime) {
					if lastAddedTimeRange.EndTime.Before(*timeRange.EndTime) {
						lastAddedTimeRange.EndTime = timeRange.EndTime
					}
				} else {
					pureRange := &model.TimeRange{
						StartTime: timeRange.StartTime,
						EndTime:   timeRange.EndTime,
					}
					timeRanges = append(timeRanges, pureRange)
				}
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
	var groupedResults []linq.Group
	linq.From(dropInfos).
		WhereT(func(dropInfo *model.DropInfo) bool { return dropInfo.ItemID.Valid }).
		GroupByT(
			func(dropInfo *model.DropInfo) int { return dropInfo.StageID },
			func(dropInfo *model.DropInfo) *model.DropInfo { return dropInfo },
		).
		ToSlice(&groupedResults)
	results := make(map[int]*model.TimeRange)
	for _, el := range groupedResults {
		stageId := el.Key.(int)
		latestDropInfo := linq.From(el.Group).
			Distinct().
			SortT(
				func(a, b *model.DropInfo) bool {
					return timeRangesMap[a.RangeID].StartTime.After(*timeRangesMap[b.RangeID].StartTime)
				}).
			First().(*model.DropInfo)
		results[stageId] = timeRangesMap[latestDropInfo.RangeID]
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

	timeRangesMap, err := legacyGetAllMaxAccumulableTimeRangesByServer(ctx, s.TimeRangeService, server)
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
	var groupedResults []linq.Group
	linq.From(combinedResults).
		GroupByT(
			func(el *model.CombinedResultForDropMatrix) int { return el.StageID },
			func(el *model.CombinedResultForDropMatrix) *model.CombinedResultForDropMatrix { return el }).ToSlice(&groupedResults)

	dropMatrixElements := make([]*model.DropMatrixElement, 0)
	for _, el := range groupedResults {
		stageId := el.Key.(int)
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
		var dropItemIds []int
		linq.From(dropInfos).
			WhereT(func(el *model.DropInfo) bool { return el.ItemID.Valid }).
			SelectT(func(el *model.DropInfo) int { return int(el.ItemID.Int64) }).
			ToSlice(&dropItemIds)
		linq.From(dropItemIds).WhereT(func(itemId int) bool { return linq.From(itemIds).Contains(itemId) }).ToSlice(&dropItemIds)
		// use a fake hashset to save item ids
		dropSet := make(map[int]struct{}, len(dropItemIds))
		for _, itemId := range dropItemIds {
			dropSet[itemId] = struct{}{}
		}

		for _, el2 := range el.Group {
			itemId := el2.(*model.CombinedResultForDropMatrix).ItemID
			quantity := el2.(*model.CombinedResultForDropMatrix).Quantity
			times := el2.(*model.CombinedResultForDropMatrix).Times
			quantityBuckets := el2.(*model.CombinedResultForDropMatrix).QuantityBuckets
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

func legacyCombinePatternQuantityAndTimesResults(
	quantityResults []*model.TotalQuantityResultForPatternMatrix, timesResults []*model.TotalTimesResult,
) []*model.CombinedResultForDropPattern {
	var firstGroupResults []linq.Group
	combinedResults := make([]*model.CombinedResultForDropPattern, 0)
	linq.From(quantityResults).
		GroupByT(
			func(result *model.TotalQuantityResultForPatternMatrix) int { return result.StageID },
			func(result *model.TotalQuantityResultForPatternMatrix) *model.TotalQuantityResultForPatternMatrix {
				return result
			}).
		ToSlice(&firstGroupResults)
	quantityResultsMap := make(map[int]map[int]int)
	for _, firstGroupElements := range firstGroupResults {
		stageId := firstGroupElements.Key.(int)
		resultsMap := make(map[int]int)
		linq.From(firstGroupElements.Group).
			ToMapByT(&resultsMap,
				func(el any) int { return el.(*model.TotalQuantityResultForPatternMatrix).PatternID },
				func(el any) int { return el.(*model.TotalQuantityResultForPatternMatrix).TotalQuantity })
		quantityResultsMap[stageId] = resultsMap
	}

	var secondGroupResults []linq.Group
	linq.From(timesResults).
		GroupByT(
			func(result *model.TotalTimesResult) int { return result.StageID },
			func(result *model.TotalTimesResult) *model.TotalTimesResult { return result }).
		ToSlice(&secondGroupResults)
	for _, secondGroupResults := range secondGroupResults {
		stageId := secondGroupResults.Key.(int)
		quantityResultsMapForOneStage := quantityResultsMap[stageId]
		for _, el := range secondGroupResults.Group {
			times := el.(*model.TotalTimesResult).TotalTimes
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

	var firstGroupResults []linq.Group
	linq.From(quantityResults).
		GroupByT(
			func(result *model.TotalQuantityResultForDropMatrix) int { return result.StageID },
			func(result *model.TotalQuantityResultForDropMatrix) *model.TotalQuantityResultForDropMatrix {
				return result
			}).
		ToSlice(&firstGroupResults)
	quantityResultsMap := make(map[int]map[int]int)
	for _, firstGroupElements := range firstGroupResults {
		stageId := firstGroupElements.Key.(int)
		resultsMap := make(map[int]int)
		linq.From(firstGroupElements.Group).
			ToMapByT(&resultsMap,
				func(el any) int { return el.(*model.TotalQuantityResultForDropMatrix).ItemID },
				func(el any) int { return el.(*model.TotalQuantityResultForDropMatrix).TotalQuantity })
		quantityResultsMap[stageId] = resultsMap
	}

	var quantityUniqCountGroupResultsByStageID []linq.Group
	linq.From(quantityUniqCountResults).
		GroupByT(func(result *model.QuantityUniqCountResultForDropMatrix) int { return result.StageID },
			func(result *model.QuantityUniqCountResultForDropMatrix) *model.QuantityUniqCountResultForDropMatrix {
				return result
			}).
		ToSlice(&quantityUniqCountGroupResultsByStageID)
	quantityUniqCountResultsMap := make(map[int]map[int]map[int]int)
	for _, quantityUniqCountGroupElements := range quantityUniqCountGroupResultsByStageID {
		stageId := quantityUniqCountGroupElements.Key.(int)
		var quantityUniqCountGroupResultsByItemID []linq.Group
		linq.From(quantityUniqCountGroupElements.Group).
			GroupByT(func(result *model.QuantityUniqCountResultForDropMatrix) int { return result.ItemID },
				func(result *model.QuantityUniqCountResultForDropMatrix) *model.QuantityUniqCountResultForDropMatrix {
					return result
				}).ToSlice(&quantityUniqCountGroupResultsByItemID)
		oneItemResultsMap := make(map[int]map[int]int)
		for _, quantityUniqCountGroupElements2 := range quantityUniqCountGroupResultsByItemID {
			itemId := quantityUniqCountGroupElements2.Key.(int)
			subMap := make(map[int]int)
			linq.From(quantityUniqCountGroupElements2.Group).
				ToMapByT(&subMap,
					func(el any) int { return el.(*model.QuantityUniqCountResultForDropMatrix).Quantity },
					func(el any) int { return el.(*model.QuantityUniqCountResultForDropMatrix).Count })
			oneItemResultsMap[itemId] = subMap
		}
		quantityUniqCountResultsMap[stageId] = oneItemResultsMap
	}

	var secondGroupResults []linq.Group
	linq.From(timesResults).
		GroupByT(
			func(result *model.TotalTimesResult) int { return result.StageID },
			func(result *model.TotalTimesResult) *model.TotalTimesResult { return result }).
		ToSlice(&secondGroupResults)
	for _, secondGroupResults := range secondGroupResults {
		stageId := secondGroupResults.Key.(int)
		quantityResultsMapForOneStage := quantityResultsMap[stageId]
		quantityUniqCountResultsMapForOneStage := quantityUniqCountResultsMap[stageId]
		for _, el := range secondGroupResults.Group {
			times := el.(*model.TotalTimesResult).TotalTimes
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
