package service

import (
	"context"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/util"
)

type DropMatrixGlobal struct {
	Config                   *appconfig.Config
	TimeRangeService         *TimeRange
	DropReportService        *DropReport
	DropInfoService          *DropInfo
	DropMatrixElementService *DropMatrixElement
	StageService             *Stage
	ItemService              *Item
}

func NewDropMatrixGlobal(
	config *appconfig.Config,
	timeRangeService *TimeRange,
	dropReportService *DropReport,
	dropInfoService *DropInfo,
	dropMatrixElementService *DropMatrixElement,
	stageService *Stage,
	itemService *Item,
) *DropMatrixGlobal {
	return &DropMatrixGlobal{
		Config:                   config,
		TimeRangeService:         timeRangeService,
		DropReportService:        dropReportService,
		DropInfoService:          dropInfoService,
		DropMatrixElementService: dropMatrixElementService,
		StageService:             stageService,
		ItemService:              itemService,
	}
}

func (s *DropMatrixGlobal) UpdateDropMatrixByGivenDate(ctx context.Context, server string, date *time.Time) error {
	dropMatrixElements, err := s.CalcDropMatrixByGivenDate(ctx, server, date, s.Config.MatrixWorkerSourceCategories)
	if err != nil {
		return err
	}
	dayNum := util.GetDayNum(date, server)
	s.DropMatrixElementService.DeleteByServerAndDayNum(ctx, server, dayNum)
	if len(dropMatrixElements) != 0 {
		s.DropMatrixElementService.BatchSaveElements(ctx, dropMatrixElements, server)
	}
	return nil
}

func (s *DropMatrixGlobal) CalcDropMatrixByGivenDate(
	ctx context.Context, server string, date *time.Time, sourceCategories []string) ([]*model.DropMatrixElement, error) {
	dropMatrixElements := make([]*model.DropMatrixElement, 0)

	start := time.UnixMilli(util.GetDayStartTime(date, server))
	end := start.Add(time.Hour * 24)
	timeRangeGiven := &model.TimeRange{
		StartTime: &start,
		EndTime:   &end,
	}

	timeRangesMap, err := s.TimeRangeService.GetAllMaxAccumulableTimeRangesByServer(ctx, "CN")
	if err != nil {
		return nil, err
	}
	stageIdsItemIdsMapByTimeRangeStr := make(map[string]map[int][]int, 0)
	for stageId, timeRangesMapByItemId := range timeRangesMap {
		for itemId, timeRanges := range timeRangesMapByItemId {
			for _, timeRange := range timeRanges {
				timeRangeStr := timeRange.String()
				if _, ok := stageIdsItemIdsMapByTimeRangeStr[timeRangeStr]; !ok {
					stageIdsItemIdsMapByTimeRangeStr[timeRangeStr] = make(map[int][]int, 0)
				}
				if _, ok := stageIdsItemIdsMapByTimeRangeStr[timeRangeStr][stageId]; !ok {
					stageIdsItemIdsMapByTimeRangeStr[timeRangeStr][stageId] = make([]int, 0)
				}
				stageIdsItemIdsMapByTimeRangeStr[timeRangeStr][stageId] = append(stageIdsItemIdsMapByTimeRangeStr[timeRangeStr][stageId], itemId)
			}
		}
	}

	for timeRangeStr, stageIdsItemIdsMap := range stageIdsItemIdsMapByTimeRangeStr {
		timeRange := model.TimeRangeFromString(timeRangeStr)
		intersection := util.GetIntersection(timeRange, timeRangeGiven)
		if intersection == nil {
			continue
		}
		for _, sourceCategory := range sourceCategories {
			queryCtx := &model.DropReportQueryContext{
				Server:             server,
				StartTime:          intersection.StartTime,
				EndTime:            intersection.EndTime,
				SourceCategory:     sourceCategory,
				ExcludeNonOneTimes: true,
				StageItemFilter:    &stageIdsItemIdsMap,
			}
			res, err := s.calcDropMatrix(ctx, queryCtx)
			if err != nil {
				return nil, err
			}
			dropMatrixElements = append(dropMatrixElements, res...)
		}
	}
	return dropMatrixElements, nil
}

func (s *DropMatrixGlobal) calcDropMatrix(ctx context.Context, queryCtx *model.DropReportQueryContext) ([]*model.DropMatrixElement, error) {
	var combinedResults []*model.CombinedResultForDropMatrix
	quantityResults, err := s.DropReportService.CalcTotalQuantityForDropMatrix(ctx, queryCtx)
	if err != nil {
		return nil, err
	}
	timesResults, err := s.DropReportService.CalcTotalTimesForDropMatrix(ctx, queryCtx)
	if err != nil {
		return nil, err
	}
	quantityUniqCountResults, err := s.DropReportService.CalcQuantityUniqCount(ctx, queryCtx)
	if err != nil {
		return nil, err
	}

	oneBatch := s.combineQuantityAndTimesResults(quantityResults, timesResults, quantityUniqCountResults)
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

func (s *DropMatrixGlobal) combineQuantityAndTimesResults(
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
				quantityBuckets := quantityUniqCountResultsMapForOneStage[itemId]
				if !s.validateQuantityBucketsAndTimes(quantityBuckets, times) {
					log.Warn().Msgf("quantity buckets and times are not matched for stage %d, item %d, please check drop pattern", stageId, itemId)
				}
				combinedResults = append(combinedResults, &model.CombinedResultForDropMatrix{
					StageID:         stageId,
					ItemID:          itemId,
					Quantity:        quantity,
					QuantityBuckets: quantityBuckets,
					Times:           times,
				})
			}
		}
	}
	return combinedResults
}

func (s *DropMatrixGlobal) validateQuantityBucketsAndTimes(quantityBuckets map[int]int, times int) bool {
	sum := 0
	for _, quantity := range quantityBuckets {
		sum += quantity
	}
	return sum <= times
}
