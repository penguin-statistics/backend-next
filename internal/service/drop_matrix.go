package service

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/pkg/errors"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
	"github.com/penguin-statistics/backend-next/internal/util"
)

/*
This service has four functions:

	1. Get Global Drop Matrix
		a. getDropMatrixElements() to get elements from DB
		b. convertDropMatrixElementsToMaxAccumulableDropMatrixQueryResult() to combine elements based max accumulable timeranges and convert to DropMatrixQueryResult
		c. apply shim for v2 (optional)

	2. Get Personal Drop Matrix
		a. calcDropMatrixForTimeRanges() to calc elements
		b. convertDropMatrixElementsToMaxAccumulableDropMatrixQueryResult() to combine elements based max accumulable timeranges and convert to DropMatrixQueryResult
		c. apply shim for v2 (optional)

	3. Get Customized Drop Matrix
		a. calcDropMatrixForTimeRanges() to calc elements
		b. convertDropMatrixElementsToDropMatrixQueryResult() to convert elements to DropMatrixQueryResult
		c. apply shim for v2 (optional)

	4. Re-calculate Global Drop Matrix
		a. calcDropMatrixForTimeRanges() for each timeRange
		b. save elements into DB
*/

type DropMatrix struct {
	TimeRangeService         *TimeRange
	DropReportService        *DropReport
	DropInfoService          *DropInfo
	DropMatrixElementService *DropMatrixElement
	StageService             *Stage
	ItemService              *Item
}

func NewDropMatrix(
	timeRangeService *TimeRange,
	dropReportService *DropReport,
	dropInfoService *DropInfo,
	dropMatrixElementService *DropMatrixElement,
	stageService *Stage,
	itemService *Item,
) *DropMatrix {
	return &DropMatrix{
		TimeRangeService:         timeRangeService,
		DropReportService:        dropReportService,
		DropInfoService:          dropInfoService,
		DropMatrixElementService: dropMatrixElementService,
		StageService:             stageService,
		ItemService:              itemService,
	}
}

// Cache: shimMaxAccumulableDropMatrixResults#server|showClosedZoned:{server}|{showClosedZones}, 24 hrs, records last modified time
func (s *DropMatrix) GetShimMaxAccumulableDropMatrixResults(ctx context.Context, server string, showClosedZones bool, stageFilterStr string, itemFilterStr string, accountId null.Int) (*modelv2.DropMatrixQueryResult, error) {
	valueFunc := func() (*modelv2.DropMatrixQueryResult, error) {
		savedDropMatrixResults, err := s.getMaxAccumulableDropMatrixResults(ctx, server, accountId)
		if err != nil {
			return nil, err
		}
		slowResults, err := s.applyShimForDropMatrixQuery(ctx, server, showClosedZones, stageFilterStr, itemFilterStr, savedDropMatrixResults)
		if err != nil {
			return nil, err
		}
		return slowResults, nil
	}

	var results modelv2.DropMatrixQueryResult
	if !accountId.Valid && stageFilterStr == "" && itemFilterStr == "" {
		key := server + constant.CacheSep + strconv.FormatBool(showClosedZones)
		calculated, err := cache.ShimMaxAccumulableDropMatrixResults.MutexGetSet(key, &results, valueFunc, 24*time.Hour)
		if err != nil {
			return nil, err
		} else if calculated {
			cache.LastModifiedTime.Set("[shimMaxAccumulableDropMatrixResults#server|showClosedZoned:"+key+"]", time.Now(), 0)
		}
		return &results, nil
	} else {
		return valueFunc()
	}
}

func (s *DropMatrix) GetShimCustomizedDropMatrixResults(ctx context.Context, server string, timeRange *model.TimeRange, stageIds []int, itemIds []int, accountId null.Int) (*modelv2.DropMatrixQueryResult, error) {
	customizedDropMatrixQueryResult, err := s.QueryDropMatrix(ctx, server, []*model.TimeRange{timeRange}, stageIds, itemIds, accountId)
	if err != nil {
		return nil, err
	}
	return s.applyShimForDropMatrixQuery(ctx, server, true, "", "", customizedDropMatrixQueryResult)
}

func (s *DropMatrix) RefreshAllDropMatrixElements(ctx context.Context, server string) error {
	toSave := []*model.DropMatrixElement{}
	allTimeRanges, err := s.TimeRangeService.GetTimeRangesByServer(ctx, server)
	if err != nil {
		return err
	}
	ch := make(chan []*model.DropMatrixElement, 15)
	var wg sync.WaitGroup

	go func() {
		for {
			m, ok := <-ch
			if !ok {
				return
			}
			toSave = append(toSave, m...)
			wg.Done()
		}
	}()

	usedTimeMap := sync.Map{}

	limiter := make(chan struct{}, runtime.NumCPU())
	wg.Add(len(allTimeRanges))
	for _, timeRange := range allTimeRanges {
		limiter <- struct{}{}
		go func(timeRange *model.TimeRange) {
			startTime := time.Now()

			timeRanges := []*model.TimeRange{timeRange}
			currentBatch, err := s.calcDropMatrixForTimeRanges(ctx, server, timeRanges, nil, nil, null.NewInt(0, false))
			if err != nil {
				return
			}

			ch <- currentBatch
			<-limiter

			usedTimeMap.Store(timeRange.RangeID, int(time.Since(startTime).Microseconds()))
		}(timeRange)
	}

	wg.Wait()
	close(ch)

	if err := s.DropMatrixElementService.BatchSaveElements(ctx, toSave, server); err != nil {
		return err
	}
	if err := cache.ShimMaxAccumulableDropMatrixResults.Delete(server + constant.CacheSep + "true"); err != nil {
		return err
	}
	if err := cache.ShimMaxAccumulableDropMatrixResults.Delete(server + constant.CacheSep + "false"); err != nil {
		return err
	}
	return nil
}

// calc DropMatrixQueryResult for customized conditions
func (s *DropMatrix) QueryDropMatrix(
	ctx context.Context, server string, timeRanges []*model.TimeRange, stageIdFilter []int, itemIdFilter []int, accountId null.Int,
) (*model.DropMatrixQueryResult, error) {
	dropMatrixElements, err := s.calcDropMatrixForTimeRanges(ctx, server, timeRanges, stageIdFilter, itemIdFilter, accountId)
	if err != nil {
		return nil, err
	}
	return s.convertDropMatrixElementsToDropMatrixQueryResult(ctx, dropMatrixElements)
}

// calc DropMatrixQueryResult for max accumulable timeranges
func (s *DropMatrix) getMaxAccumulableDropMatrixResults(ctx context.Context, server string, accountId null.Int) (*model.DropMatrixQueryResult, error) {
	dropMatrixElements, err := s.getDropMatrixElements(ctx, server, accountId)
	if err != nil {
		return nil, err
	}
	return s.convertDropMatrixElementsToMaxAccumulableDropMatrixQueryResult(ctx, server, dropMatrixElements)
}

// For global, get elements from DB; For personal, calc elements
func (s *DropMatrix) getDropMatrixElements(ctx context.Context, server string, accountId null.Int) ([]*model.DropMatrixElement, error) {
	if accountId.Valid {
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
		return s.calcDropMatrixForTimeRanges(ctx, server, timeRanges, nil, nil, accountId)
	} else {
		return s.DropMatrixElementService.GetElementsByServer(ctx, server)
	}
}

func (s *DropMatrix) calcDropMatrixForTimeRanges(
	ctx context.Context, server string, timeRanges []*model.TimeRange, stageIdFilter []int, itemIdFilter []int, accountId null.Int,
) ([]*model.DropMatrixElement, error) {
	dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, timeRanges, stageIdFilter, itemIdFilter)
	if err != nil {
		return nil, err
	}

	var combinedResults []*model.CombinedResultForDropMatrix
	for _, timeRange := range timeRanges {
		stageIdItemIdMap := util.GetStageIdItemIdMapFromDropInfos(dropInfos)
		quantityResults, err := s.DropReportService.CalcTotalQuantityForDropMatrix(ctx, server, timeRange, stageIdItemIdMap, accountId)
		if err != nil {
			return nil, err
		}
		timesResults, err := s.DropReportService.CalcTotalTimesForDropMatrix(ctx, server, timeRange, util.GetStageIdsFromDropInfos(dropInfos), accountId)
		if err != nil {
			return nil, err
		}
		quantityUniqCountResults, err := s.DropReportService.CalcQuantityUniqCount(ctx, server, timeRange, stageIdItemIdMap, accountId)
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
				combinedResults = append(combinedResults, &model.CombinedResultForDropMatrix{
					StageID:         stageId,
					ItemID:          itemId,
					Quantity:        quantity,
					QuantityBuckets: quantityUniqCountResultsMapForOneStage[itemId],
					Times:           times,
					TimeRange:       timeRange,
				})
			}
		}
	}
	return combinedResults
}

func (s *DropMatrix) convertDropMatrixElementsToMaxAccumulableDropMatrixQueryResult(
	ctx context.Context, server string, dropMatrixElements []*model.DropMatrixElement,
) (*model.DropMatrixQueryResult, error) {
	elementsMap := util.GetDropMatrixElementsMap(dropMatrixElements)
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
					StdDev:   util.RoundFloat64(util.CalcStdDevFromQuantityBuckets(element.QuantityBuckets, element.Times), constant.StdDevDigits),
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

func (s *DropMatrix) combineDropMatrixResults(a, b *model.OneDropMatrixElement) (*model.OneDropMatrixElement, error) {
	if a.StageID != b.StageID {
		return nil, errors.New("stageId not match")
	}
	if a.ItemID != b.ItemID {
		return nil, errors.New("itemId not match")
	}
	result := &model.OneDropMatrixElement{
		StageID:  a.StageID,
		ItemID:   a.ItemID,
		Quantity: a.Quantity + b.Quantity,
		Times:    a.Times + b.Times,
		StdDev: util.RoundFloat64(
			util.CombineTwoBundles(
				s.convertOneDropMatrixElementToStatsBundle(a),
				s.convertOneDropMatrixElementToStatsBundle(b),
			).StdDev, constant.StdDevDigits),
	}
	return result, nil
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
				StdDev:    util.RoundFloat64(util.CalcStdDevFromQuantityBuckets(dropMatrixElement.QuantityBuckets, dropMatrixElement.Times), constant.StdDevDigits),
				TimeRange: timeRange,
			})
		}
	}
	return dropMatrixQueryResult, nil
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

func (s *DropMatrix) convertOneDropMatrixElementToStatsBundle(el *model.OneDropMatrixElement) *util.StatsBundle {
	return &util.StatsBundle{
		N:      el.Times,
		Avg:    float64(el.Quantity) / float64(el.Times),
		StdDev: el.StdDev,
	}
}
