package service

import (
	"context"
	"strconv"
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/ahmetb/go-linq/v3"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/util"
)

type DropMatrix struct {
	Config                   *appconfig.Config
	TimeRangeService         *TimeRange
	DropReportService        *DropReport
	DropInfoService          *DropInfo
	DropMatrixElementService *DropMatrixElement
	StageService             *Stage
	ItemService              *Item
}

func NewDropMatrix(
	config *appconfig.Config,
	timeRangeService *TimeRange,
	dropReportService *DropReport,
	dropInfoService *DropInfo,
	dropMatrixElementService *DropMatrixElement,
	stageService *Stage,
	itemService *Item,
) *DropMatrix {
	return &DropMatrix{
		Config:                   config,
		TimeRangeService:         timeRangeService,
		DropReportService:        dropReportService,
		DropInfoService:          dropInfoService,
		DropMatrixElementService: dropMatrixElementService,
		StageService:             stageService,
		ItemService:              itemService,
	}
}

// =========== Global & Personal, Max Accumulable ===========

// Cache: shimGlobalDropMatrix#server|showClosedZoned|sourceCategory:{server}|{showClosedZones}|{sourceCategory}, 24 hrs, records last modified time
// Called by frontend, used for both global and personal, only for max accumulable results
func (s *DropMatrix) GetShimDropMatrix(
	ctx context.Context, server string, showClosedZones bool, stageFilterStr string, itemFilterStr string, accountId null.Int, sourceCategory string,
) (*modelv2.DropMatrixQueryResult, error) {
	valueFunc := func() (*modelv2.DropMatrixQueryResult, error) {
		var dropMatrixQueryResult *model.DropMatrixQueryResult
		var err error
		if accountId.Valid {
			dropMatrixQueryResult, err = s.getMaxAccumulableDropMatrixResults(ctx, server, accountId, sourceCategory)
		} else {
			dropMatrixQueryResult, err = s.calcGlobalDropMatrix(ctx, server, sourceCategory)
		}
		if err != nil {
			return nil, err
		}
		slowResults, err := s.applyShimForDropMatrixQuery(ctx, server, showClosedZones, stageFilterStr, itemFilterStr, dropMatrixQueryResult)
		if err != nil {
			return nil, err
		}
		return slowResults, nil
	}

	var results modelv2.DropMatrixQueryResult
	if !accountId.Valid && stageFilterStr == "" && itemFilterStr == "" {
		key := server + constant.CacheSep + strconv.FormatBool(showClosedZones) + constant.CacheSep + sourceCategory
		calculated, err := cache.ShimGlobalDropMatrix.MutexGetSet(key, &results, valueFunc, 24*time.Hour)
		if err != nil {
			return nil, err
		} else if calculated {
			cache.LastModifiedTime.Set("[shimGlobalDropMatrix#server|showClosedZoned|sourceCategory:"+key+"]", time.Now(), 0)
		}
	} else {
		return valueFunc()
	}
	return &results, nil
}

// =========== Global Max Accumulable ===========

// Calc today's drop matrix elements and save to DB
// Called by worker
func (s *DropMatrix) RunCalcDropMatrixJob(ctx context.Context, server string) error {
	date := time.Now()
	endTime := time.Now()
	dropMatrixElements, err := s.calcDropMatrixByGivenDate(ctx, server, &date, &endTime, s.Config.MatrixWorkerSourceCategories)
	if err != nil {
		return err
	}
	dayNum := util.GetDayNum(&date, server)
	exists, err := s.DropMatrixElementService.IsExistByServerAndDayNum(ctx, server, dayNum)
	if err != nil {
		return err
	}
	s.DropMatrixElementService.DeleteByServerAndDayNum(ctx, server, dayNum)

	if len(dropMatrixElements) != 0 {
		s.DropMatrixElementService.BatchSaveElements(ctx, dropMatrixElements, server)
	}

	// If this is the first time we run the job for this server at this day, we need to update the drop matrix for the previous day.
	if !exists {
		yesterday := date.Add(time.Hour * -24)
		dropMatrixElementsForYesterday, err := s.calcDropMatrixByGivenDate(ctx, server, &yesterday, nil, s.Config.MatrixWorkerSourceCategories)
		if err != nil {
			return err
		}
		s.DropMatrixElementService.DeleteByServerAndDayNum(ctx, server, dayNum-1)
		if len(dropMatrixElementsForYesterday) != 0 {
			s.DropMatrixElementService.BatchSaveElements(ctx, dropMatrixElementsForYesterday, server)
		}
	}

	for _, sourceCategory := range s.Config.MatrixWorkerSourceCategories {
		if err := cache.ShimGlobalDropMatrix.Delete(server + constant.CacheSep + "true" + constant.CacheSep + sourceCategory); err != nil {
			return err
		}
		if err := cache.ShimGlobalDropMatrix.Delete(server + constant.CacheSep + "false" + constant.CacheSep + sourceCategory); err != nil {
			return err
		}
	}
	if err := cache.ShimTrend.Delete(server); err != nil {
		return err
	}
	return nil
}

// Update drop matrix elements for a given date (entire day)
// Called by admin api
func (s *DropMatrix) UpdateDropMatrixByGivenDate(ctx context.Context, server string, date *time.Time) error {
	dropMatrixElements, err := s.calcDropMatrixByGivenDate(ctx, server, date, nil, s.Config.MatrixWorkerSourceCategories)
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

/**
 * Calculate drop matrix for a given date
 * @param date indicates the date to calculate drop matrix
 * @param endTime if nil, the calculation will be done for the entire day; otherwise, the calculation will be done for the partial day
 */
func (s *DropMatrix) calcDropMatrixByGivenDate(
	ctx context.Context, server string, date *time.Time, endTime *time.Time, sourceCategories []string) ([]*model.DropMatrixElement, error) {
	dropMatrixElements := make([]*model.DropMatrixElement, 0)

	start := time.UnixMilli(util.GetDayStartTime(date, server))
	startNextDay := start.Add(time.Hour * 24)
	end := lo.Ternary(endTime == nil, &startNextDay, endTime)

	timeRangeGiven := &model.TimeRange{
		StartTime: &start,
		EndTime:   end,
	}

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
			res, err := s.calcDropMatrix(ctx, queryCtx)
			if err != nil {
				return nil, err
			}
			dropMatrixElements = append(dropMatrixElements, res...)
		}
	}
	return dropMatrixElements, nil
}

func (s *DropMatrix) calcDropMatrix(ctx context.Context, queryCtx *model.DropReportQueryContext) ([]*model.DropMatrixElement, error) {
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

	oneBatch := s.combineQuantityAndTimesResults(quantityResults, timesResults, quantityUniqCountResults, nil)
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

func (s *DropMatrix) calcGlobalDropMatrix(ctx context.Context, server string, sourceCategory string) (*model.DropMatrixQueryResult, error) {
	timesResults, err := s.DropMatrixElementService.GetAllTimesForGlobalDropMatrixMapByStageId(ctx, server, sourceCategory)
	if err != nil {
		return nil, err
	}
	quantityResults, err := s.DropMatrixElementService.GetAllQuantitiesForGlobalDropMatrixMapByStageIdAndItemId(ctx, server, sourceCategory)
	if err != nil {
		return nil, err
	}
	quantityUniqCountResults, err := s.DropMatrixElementService.GetAllQuantityBucketsForGlobalDropMatrixMapByStageIdAndItemId(ctx, server, sourceCategory)
	if err != nil {
		return nil, err
	}
	maxAccumulableTimeRanges, err := s.TimeRangeService.GetAllMaxAccumulableTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}

	finalResult := &model.DropMatrixQueryResult{
		Matrix: make([]*model.OneDropMatrixElement, 0),
	}
	for stageId, subMap := range quantityResults {
		timesResult := timesResults[stageId]
		for itemId, quantityResult := range subMap {
			quantityUniqCountResult := quantityUniqCountResults[stageId][itemId]
			maxAccumulableTimeRanges := maxAccumulableTimeRanges[stageId][itemId]
			oneDropMatrixElement := &model.OneDropMatrixElement{
				StageID:   stageId,
				ItemID:    itemId,
				Times:     timesResult.Times,
				Quantity:  quantityResult.Quantity,
				StdDev:    util.RoundFloat64(util.CalcStdDevFromQuantityBuckets(quantityUniqCountResult.QuantityBuckets, timesResult.Times, false), constant.StdDevDigits),
				TimeRange: maxAccumulableTimeRanges[0],
			}
			finalResult.Matrix = append(finalResult.Matrix, oneDropMatrixElement)
		}
	}
	return finalResult, nil
}

// =========== Personal Max Accumulable ===========

func (s *DropMatrix) getMaxAccumulableDropMatrixResults(ctx context.Context, server string, accountId null.Int, sourceCategory string) (*model.DropMatrixQueryResult, error) {
	dropMatrixElements, err := s.getDropMatrixElements(ctx, server, accountId, sourceCategory)
	if err != nil {
		return nil, err
	}
	return s.convertDropMatrixElementsToMaxAccumulableDropMatrixQueryResult(ctx, server, dropMatrixElements)
}

func (s *DropMatrix) getDropMatrixElements(ctx context.Context, server string, accountId null.Int, sourceCategory string) ([]*model.DropMatrixElement, error) {
	unifiedEndTime := time.Now()
	maxAccumulableTimeRanges, err := s.TimeRangeService.GetMaxAccumulableTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	timeRanges := make([]*model.TimeRange, 0)

	timeRangesMap := make(map[int]*model.TimeRange)
	for _, maxAccumulableTimeRangesForOneStage := range maxAccumulableTimeRanges {
		for _, timeRanges := range maxAccumulableTimeRangesForOneStage {
			for _, timeRange := range timeRanges {
				timeRangesMap[timeRange.RangeID] = timeRange
			}
		}
	}
	for _, timeRange := range timeRangesMap {
		timeRanges = append(timeRanges, timeRange)
	}
	dropMatrixElements, err := s.calcDropMatrixForTimeRanges(ctx, server, timeRanges, nil, nil, accountId, sourceCategory, &unifiedEndTime)
	if err != nil {
		return nil, err
	}
	return dropMatrixElements, nil
}

func (s *DropMatrix) convertDropMatrixElementsToMaxAccumulableDropMatrixQueryResult(
	ctx context.Context, server string, dropMatrixElements []*model.DropMatrixElement,
) (*model.DropMatrixQueryResult, error) {
	elementsMap := util.GetDropMatrixElementsMap(dropMatrixElements, true)
	result := &model.DropMatrixQueryResult{
		Matrix: make([]*model.OneDropMatrixElement, 0),
	}
	maxAccumulableTimeRanges, err := s.TimeRangeService.GetMaxAccumulableTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	for stageId, maxAccumulableTimeRangesForOneStage := range maxAccumulableTimeRanges {
		subMapByItemId := elementsMap[stageId]
		for itemId, timeRanges := range maxAccumulableTimeRangesForOneStage {
			subMapByRangeId := subMapByItemId[itemId]
			startTime := timeRanges[0].StartTime
			endTime := timeRanges[0].EndTime
			var combinedDropMatrixResult *model.OneDropMatrixElement
			combinedDropMatrixResult = nil
			for _, timeRange := range timeRanges {
				element, ok := subMapByRangeId[timeRange.RangeID]
				if !ok {
					continue
				}
				oneElementResult := &model.OneDropMatrixElement{
					StageID:  stageId,
					ItemID:   itemId,
					Quantity: element.Quantity,
					Times:    element.Times,
					StdDev:   util.RoundFloat64(util.CalcStdDevFromQuantityBuckets(element.QuantityBuckets, element.Times, false), constant.StdDevDigits),
				}
				if timeRange.StartTime.Before(*startTime) {
					startTime = timeRange.StartTime
				}
				if timeRange.EndTime.After(*endTime) {
					endTime = timeRange.EndTime
				}
				if combinedDropMatrixResult == nil {
					combinedDropMatrixResult = oneElementResult
				} else {
					combinedDropMatrixResult, err = s.combineDropMatrixResults(combinedDropMatrixResult, oneElementResult)
					if err != nil {
						return nil, err
					}
				}
			}
			if combinedDropMatrixResult != nil {
				combinedDropMatrixResult.TimeRange = &model.TimeRange{
					StartTime: startTime,
					EndTime:   endTime,
				}
				result.Matrix = append(result.Matrix, combinedDropMatrixResult)
			}
		}
	}
	return result, nil
}

// =========== Customized ===========

func (s *DropMatrix) GetShimCustomizedDropMatrixResults(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIds []int, itemIds []int, accountId null.Int, sourceCategory string,
) (*modelv2.DropMatrixQueryResult, error) {
	unifiedEndTime := time.Now()
	timeRanges := []*model.TimeRange{timeRange}
	dropMatrixElements, err := s.calcDropMatrixForTimeRanges(ctx, server, timeRanges, stageIds, itemIds, accountId, sourceCategory, &unifiedEndTime)
	if err != nil {
		return nil, err
	}
	customizedDropMatrixQueryResult, err := s.convertDropMatrixElementsToDropMatrixQueryResult(ctx, dropMatrixElements)
	if err != nil {
		return nil, err
	}
	return s.applyShimForDropMatrixQuery(ctx, server, true, "", "", customizedDropMatrixQueryResult)
}

func (s *DropMatrix) convertDropMatrixElementsToDropMatrixQueryResult(ctx context.Context, dropMatrixElements []*model.DropMatrixElement) (*model.DropMatrixQueryResult, error) {
	dropMatrixQueryResult := &model.DropMatrixQueryResult{
		Matrix: make([]*model.OneDropMatrixElement, 0),
	}
	var groupedResults []linq.Group
	linq.From(dropMatrixElements).
		GroupByT(
			func(el *model.DropMatrixElement) int { return el.RangeID },
			func(el *model.DropMatrixElement) *model.DropMatrixElement { return el },
		).
		ToSlice(&groupedResults)
	for _, group := range groupedResults {
		rangeId := group.Key.(int)
		var timeRange *model.TimeRange
		if rangeId == 0 {
			timeRange = group.Group[0].(*model.DropMatrixElement).TimeRange
		} else {
			tr, err := s.TimeRangeService.GetTimeRangeById(ctx, rangeId)
			if err != nil {
				return nil, err
			}
			timeRange = tr
		}

		for _, el := range group.Group {
			dropMatrixElement := el.(*model.DropMatrixElement)
			dropMatrixQueryResult.Matrix = append(dropMatrixQueryResult.Matrix, &model.OneDropMatrixElement{
				StageID:   dropMatrixElement.StageID,
				ItemID:    dropMatrixElement.ItemID,
				Quantity:  dropMatrixElement.Quantity,
				Times:     dropMatrixElement.Times,
				StdDev:    util.RoundFloat64(util.CalcStdDevFromQuantityBuckets(dropMatrixElement.QuantityBuckets, dropMatrixElement.Times, false), constant.StdDevDigits),
				TimeRange: timeRange,
			})
		}
	}
	return dropMatrixQueryResult, nil
}

// =========== Helpers ===========

// Called in Personal Max Accumulable and Customized
func (s *DropMatrix) calcDropMatrixForTimeRanges(
	ctx context.Context, server string, timeRanges []*model.TimeRange, stageIdFilter []int, itemIdFilter []int, accountId null.Int, sourceCategory string, unifiedEndTime *time.Time,
) ([]*model.DropMatrixElement, error) {
	// For one time range whose end time is FakeEndTimeMilli, we will make separate query to get times, quantity and quantity buckets.
	// We need to make sure they are queried based on the same set of drop reports. So we will use a unified end time instead of FakeEndTimeMilli.
	for _, timeRange := range timeRanges {
		if timeRange.EndTime.After(*unifiedEndTime) {
			timeRange.EndTime = unifiedEndTime
		}
	}

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
		oneBatch := s.combineQuantityAndTimesResults(quantityResults, timesResults, quantityUniqCountResults, timeRange)
		combinedResults = append(combinedResults, oneBatch...)
	}

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
		var groupedResults2 []linq.Group
		linq.From(el.Group).
			GroupByT(
				func(el *model.CombinedResultForDropMatrix) int { return el.TimeRange.RangeID },
				func(el *model.CombinedResultForDropMatrix) *model.CombinedResultForDropMatrix { return el }).
			ToSlice(&groupedResults2)
		for _, el2 := range groupedResults2 {
			rangeId := el2.Key.(int)
			timeRange := el2.Group[0].(*model.CombinedResultForDropMatrix).TimeRange

			// get all item ids which are dropped in this stage and in this time range
			var dropItemIds []int
			if rangeId == 0 {
				// rangeId == 0 means it is a customized time range instead of a time range from the database
				dropInfosForSpecialTimeRange, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, []*model.TimeRange{el2.Group[0].(*model.CombinedResultForDropMatrix).TimeRange}, []int{stageId}, itemIdFilter)
				if err != nil {
					return nil, err
				}
				linq.From(dropInfosForSpecialTimeRange).
					WhereT(func(el *model.DropInfo) bool { return el.ItemID.Valid }).
					SelectT(func(el *model.DropInfo) int { return int(el.ItemID.Int64) }).
					ToSlice(&dropItemIds)
			} else {
				dropItemIds, _ = s.DropInfoService.GetItemDropSetByStageIdAndRangeId(ctx, server, stageId, rangeId)
			}

			// if item id filter is applied, then filter the drop item ids
			if len(itemIdFilter) > 0 {
				linq.From(dropItemIds).WhereT(func(itemId int) bool { return linq.From(itemIdFilter).Contains(itemId) }).ToSlice(&dropItemIds)
			}

			// use a fake hashset to save item ids
			dropSet := make(map[int]struct{}, len(dropItemIds))
			for _, itemId := range dropItemIds {
				dropSet[itemId] = struct{}{}
			}

			for _, el3 := range el2.Group {
				itemId := el3.(*model.CombinedResultForDropMatrix).ItemID
				quantity := el3.(*model.CombinedResultForDropMatrix).Quantity
				times := el3.(*model.CombinedResultForDropMatrix).Times
				quantityBuckets := el3.(*model.CombinedResultForDropMatrix).QuantityBuckets
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

func (s *DropMatrix) combineQuantityAndTimesResults(
	quantityResults []*model.TotalQuantityResultForDropMatrix, timesResults []*model.TotalTimesResult,
	quantityUniqCountResults []*model.QuantityUniqCountResultForDropMatrix, timeRange *model.TimeRange,
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
				combinedResultForDropMatrix := &model.CombinedResultForDropMatrix{
					StageID:         stageId,
					ItemID:          itemId,
					Quantity:        quantity,
					QuantityBuckets: quantityBuckets,
					Times:           times,
				}
				if timeRange != nil {
					combinedResultForDropMatrix.TimeRange = timeRange
				}
				combinedResults = append(combinedResults, combinedResultForDropMatrix)
			}
		}
	}
	return combinedResults
}

func (s *DropMatrix) validateQuantityBucketsAndTimes(quantityBuckets map[int]int, times int) bool {
	sum := 0
	for _, quantity := range quantityBuckets {
		sum += quantity
	}
	return sum <= times
}

func (s *DropMatrix) combineDropMatrixResults(a, b *model.OneDropMatrixElement) (*model.OneDropMatrixElement, error) {
	if a.StageID != b.StageID {
		return nil, errors.New("stageId not match")
	}
	if a.ItemID != b.ItemID {
		return nil, errors.New("itemId not match")
	}
	bundleA, err := s.convertOneDropMatrixElementToStatsBundle(a)
	if err != nil {
		return nil, err
	}
	bundleB, err := s.convertOneDropMatrixElementToStatsBundle(b)
	if err != nil {
		return nil, err
	}
	result := &model.OneDropMatrixElement{
		StageID:  a.StageID,
		ItemID:   a.ItemID,
		Quantity: a.Quantity + b.Quantity,
		Times:    a.Times + b.Times,
		StdDev: util.RoundFloat64(
			util.CombineTwoBundles(
				bundleA,
				bundleB,
			).StdDev, constant.StdDevDigits),
	}
	return result, nil
}

func (s *DropMatrix) convertOneDropMatrixElementToStatsBundle(el *model.OneDropMatrixElement) (*util.StatsBundle, error) {
	if el.Times == 0 {
		return nil, errors.New("times should not be 0")
	}
	return &util.StatsBundle{
		N:      el.Times,
		Avg:    float64(el.Quantity) / float64(el.Times),
		StdDev: el.StdDev,
	}, nil
}

func (s *DropMatrix) applyShimForDropMatrixQuery(ctx context.Context, server string, showClosedZones bool, stageFilterStr, itemFilterStr string, queryResult *model.DropMatrixQueryResult) (*modelv2.DropMatrixQueryResult, error) {
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
		currentDropInfos, err := s.DropInfoService.GetCurrentDropInfosByServer(ctx, server)
		if err != nil {
			return nil, err
		}
		linq.From(currentDropInfos).SelectT(func(el *model.DropInfo) int { return el.StageID }).Distinct().ToSlice(&openingStageIds)
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
		if !showClosedZones && !linq.From(openingStageIds).Contains(el.StageID) {
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

		endTime := null.NewInt(el.TimeRange.EndTime.UnixMilli(), true)
		oneDropMatrixElement := modelv2.OneDropMatrixElement{
			StageID:   stage.ArkStageID,
			ItemID:    item.ArkItemID,
			Quantity:  el.Quantity,
			Times:     el.Times,
			StdDev:    el.StdDev,
			StartTime: el.TimeRange.StartTime.UnixMilli(),
			EndTime:   endTime,
		}
		if oneDropMatrixElement.EndTime.Int64 == constant.FakeEndTimeMilli {
			oneDropMatrixElement.EndTime = null.NewInt(0, false)
		}
		results.Matrix = append(results.Matrix, &oneDropMatrixElement)
	}
	return results, nil
}
