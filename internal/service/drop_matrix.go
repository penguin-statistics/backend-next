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

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	modelv2 "github.com/penguin-statistics/backend-next/internal/models/v2"
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

type DropMatrixService struct {
	TimeRangeService         *TimeRangeService
	DropReportService        *DropReportService
	DropInfoService          *DropInfoService
	DropMatrixElementService *DropMatrixElementService
	StageService             *StageService
	ItemService              *ItemService
}

func NewDropMatrixService(
	timeRangeService *TimeRangeService,
	dropReportService *DropReportService,
	dropInfoService *DropInfoService,
	dropMatrixElementService *DropMatrixElementService,
	stageService *StageService,
	itemService *ItemService,
) *DropMatrixService {
	return &DropMatrixService{
		TimeRangeService:         timeRangeService,
		DropReportService:        dropReportService,
		DropInfoService:          dropInfoService,
		DropMatrixElementService: dropMatrixElementService,
		StageService:             stageService,
		ItemService:              itemService,
	}
}

// Cache: shimMaxAccumulableDropMatrixResults#server|showClosedZoned:{server}|{showClosedZones}, 24 hrs, records last modified time
func (s *DropMatrixService) GetShimMaxAccumulableDropMatrixResults(ctx context.Context, server string, showClosedZones bool, stageFilterStr string, itemFilterStr string, accountId null.Int) (*modelv2.DropMatrixQueryResult, error) {
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
		key := server + constants.CacheSep + strconv.FormatBool(showClosedZones)
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

func (s *DropMatrixService) GetShimCustomizedDropMatrixResults(ctx context.Context, server string, timeRange *models.TimeRange, stageIds []int, itemIds []int, accountId null.Int) (*modelv2.DropMatrixQueryResult, error) {
	customizedDropMatrixQueryResult, err := s.QueryDropMatrix(ctx, server, []*models.TimeRange{timeRange}, stageIds, itemIds, accountId)
	if err != nil {
		return nil, err
	}
	return s.applyShimForDropMatrixQuery(ctx, server, true, "", "", customizedDropMatrixQueryResult)
}

func (s *DropMatrixService) RefreshAllDropMatrixElements(ctx context.Context, server string) error {
	toSave := []*models.DropMatrixElement{}
	allTimeRanges, err := s.TimeRangeService.GetTimeRangesByServer(ctx, server)
	if err != nil {
		return err
	}
	ch := make(chan []*models.DropMatrixElement, 15)
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
		go func(timeRange *models.TimeRange) {
			startTime := time.Now()

			timeRanges := []*models.TimeRange{timeRange}
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
	if err := cache.ShimMaxAccumulableDropMatrixResults.Delete(server + constants.CacheSep + "true"); err != nil {
		return err
	}
	if err := cache.ShimMaxAccumulableDropMatrixResults.Delete(server + constants.CacheSep + "false"); err != nil {
		return err
	}
	return nil
}

// calc DropMatrixQueryResult for customized conditions
func (s *DropMatrixService) QueryDropMatrix(
	ctx context.Context, server string, timeRanges []*models.TimeRange, stageIdFilter []int, itemIdFilter []int, accountId null.Int,
) (*models.DropMatrixQueryResult, error) {
	dropMatrixElements, err := s.calcDropMatrixForTimeRanges(ctx, server, timeRanges, stageIdFilter, itemIdFilter, accountId)
	if err != nil {
		return nil, err
	}
	return s.convertDropMatrixElementsToDropMatrixQueryResult(ctx, dropMatrixElements)
}

// calc DropMatrixQueryResult for max accumulable timeranges
func (s *DropMatrixService) getMaxAccumulableDropMatrixResults(ctx context.Context, server string, accountId null.Int) (*models.DropMatrixQueryResult, error) {
	dropMatrixElements, err := s.getDropMatrixElements(ctx, server, accountId)
	if err != nil {
		return nil, err
	}
	return s.convertDropMatrixElementsToMaxAccumulableDropMatrixQueryResult(ctx, server, dropMatrixElements)
}

// For global, get elements from DB; For personal, calc elements
func (s *DropMatrixService) getDropMatrixElements(ctx context.Context, server string, accountId null.Int) ([]*models.DropMatrixElement, error) {
	if accountId.Valid {
		maxAccumulableTimeRanges, err := s.TimeRangeService.GetMaxAccumulableTimeRangesByServer(ctx, server)
		if err != nil {
			return nil, err
		}
		timeRanges := make([]*models.TimeRange, 0)

		timeRangesMap := make(map[int]*models.TimeRange)
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

func (s *DropMatrixService) calcDropMatrixForTimeRanges(
	ctx context.Context, server string, timeRanges []*models.TimeRange, stageIdFilter []int, itemIdFilter []int, accountId null.Int,
) ([]*models.DropMatrixElement, error) {
	dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, timeRanges, stageIdFilter, itemIdFilter)
	if err != nil {
		return nil, err
	}

	var combinedResults []*models.CombinedResultForDropMatrix
	for _, timeRange := range timeRanges {
		quantityResults, err := s.DropReportService.CalcTotalQuantityForDropMatrix(ctx, server, timeRange, util.GetStageIdItemIdMapFromDropInfos(dropInfos), accountId)
		if err != nil {
			return nil, err
		}
		timesResults, err := s.DropReportService.CalcTotalTimesForDropMatrix(ctx, server, timeRange, util.GetStageIdsFromDropInfos(dropInfos), accountId)
		if err != nil {
			return nil, err
		}
		oneBatch := s.combineQuantityAndTimesResults(quantityResults, timesResults, timeRange)
		combinedResults = append(combinedResults, oneBatch...)
	}

	// save stage times for later use
	stageTimesMap := map[int]int{}

	// grouping results by stage id
	var groupedResults []linq.Group
	linq.From(combinedResults).
		GroupByT(
			func(el *models.CombinedResultForDropMatrix) int { return el.StageID },
			func(el *models.CombinedResultForDropMatrix) *models.CombinedResultForDropMatrix { return el }).ToSlice(&groupedResults)

	dropMatrixElements := make([]*models.DropMatrixElement, 0)
	for _, el := range groupedResults {
		stageId := el.Key.(int)
		var groupedResults2 []linq.Group
		linq.From(el.Group).
			GroupByT(
				func(el *models.CombinedResultForDropMatrix) int { return el.TimeRange.RangeID },
				func(el *models.CombinedResultForDropMatrix) *models.CombinedResultForDropMatrix { return el }).
			ToSlice(&groupedResults2)
		for _, el2 := range groupedResults2 {
			rangeId := el2.Key.(int)
			timeRange := el2.Group[0].(*models.CombinedResultForDropMatrix).TimeRange

			// get all item ids which are dropped in this stage and in this time range
			var dropItemIds []int
			if rangeId == 0 {
				// rangeId == 0 means it is a customized time range instead of a time range from the database
				dropInfosForSpecialTimeRange, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, []*models.TimeRange{el2.Group[0].(*models.CombinedResultForDropMatrix).TimeRange}, []int{stageId}, itemIdFilter)
				if err != nil {
					return nil, err
				}
				linq.From(dropInfosForSpecialTimeRange).
					WhereT(func(el *models.DropInfo) bool { return el.ItemID.Valid }).
					SelectT(func(el *models.DropInfo) int { return int(el.ItemID.Int64) }).
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
				itemId := el3.(*models.CombinedResultForDropMatrix).ItemID
				quantity := el3.(*models.CombinedResultForDropMatrix).Quantity
				times := el3.(*models.CombinedResultForDropMatrix).Times
				dropMatrixElement := models.DropMatrixElement{
					StageID:  stageId,
					ItemID:   itemId,
					RangeID:  rangeId,
					Quantity: quantity,
					Times:    times,
					Server:   server,
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
				dropMatrixElementWithZeroQuantity := models.DropMatrixElement{
					StageID:  stageId,
					ItemID:   itemId,
					RangeID:  rangeId,
					Quantity: 0,
					Times:    stageTimesMap[stageId],
					Server:   server,
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

func (s *DropMatrixService) combineQuantityAndTimesResults(
	quantityResults []*models.TotalQuantityResultForDropMatrix, timesResults []*models.TotalTimesResult, timeRange *models.TimeRange,
) []*models.CombinedResultForDropMatrix {
	var firstGroupResults []linq.Group
	combinedResults := make([]*models.CombinedResultForDropMatrix, 0)
	linq.From(quantityResults).
		GroupByT(
			func(result *models.TotalQuantityResultForDropMatrix) int { return result.StageID },
			func(result *models.TotalQuantityResultForDropMatrix) *models.TotalQuantityResultForDropMatrix {
				return result
			}).
		ToSlice(&firstGroupResults)
	quantityResultsMap := make(map[int]map[int]int)
	for _, firstGroupElements := range firstGroupResults {
		stageId := firstGroupElements.Key.(int)
		resultsMap := make(map[int]int)
		linq.From(firstGroupElements.Group).
			ToMapByT(&resultsMap,
				func(el any) int { return el.(*models.TotalQuantityResultForDropMatrix).ItemID },
				func(el any) int { return el.(*models.TotalQuantityResultForDropMatrix).TotalQuantity })
		quantityResultsMap[stageId] = resultsMap
	}

	var secondGroupResults []linq.Group
	linq.From(timesResults).
		GroupByT(
			func(result *models.TotalTimesResult) int { return result.StageID },
			func(result *models.TotalTimesResult) *models.TotalTimesResult { return result }).
		ToSlice(&secondGroupResults)
	for _, secondGroupResults := range secondGroupResults {
		stageId := secondGroupResults.Key.(int)
		quantityResultsMapForOneStage := quantityResultsMap[stageId]
		for _, el := range secondGroupResults.Group {
			times := el.(*models.TotalTimesResult).TotalTimes
			for itemId, quantity := range quantityResultsMapForOneStage {
				combinedResults = append(combinedResults, &models.CombinedResultForDropMatrix{
					StageID:   stageId,
					ItemID:    itemId,
					Quantity:  quantity,
					Times:     times,
					TimeRange: timeRange,
				})
			}
		}
	}
	return combinedResults
}

func (s *DropMatrixService) convertDropMatrixElementsToMaxAccumulableDropMatrixQueryResult(
	ctx context.Context, server string, dropMatrixElements []*models.DropMatrixElement,
) (*models.DropMatrixQueryResult, error) {
	elementsMap := util.GetDropMatrixElementsMap(dropMatrixElements)
	result := &models.DropMatrixQueryResult{
		Matrix: make([]*models.OneDropMatrixElement, 0),
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
			var combinedDropMatrixResult *models.OneDropMatrixElement
			combinedDropMatrixResult = nil
			for _, timeRange := range timeRanges {
				element, ok := subMapByRangeId[timeRange.RangeID]
				if !ok {
					continue
				}
				oneElementResult := &models.OneDropMatrixElement{
					StageID:  stageId,
					ItemID:   itemId,
					Quantity: element.Quantity,
					Times:    element.Times,
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
				combinedDropMatrixResult.TimeRange = &models.TimeRange{
					StartTime: startTime,
					EndTime:   endTime,
				}
				result.Matrix = append(result.Matrix, combinedDropMatrixResult)
			}
		}
	}
	return result, nil
}

func (s *DropMatrixService) combineDropMatrixResults(a, b *models.OneDropMatrixElement) (*models.OneDropMatrixElement, error) {
	if a.StageID != b.StageID {
		return nil, errors.New("stageId not match")
	}
	if a.ItemID != b.ItemID {
		return nil, errors.New("itemId not match")
	}
	result := &models.OneDropMatrixElement{
		StageID:  a.StageID,
		ItemID:   a.ItemID,
		Quantity: a.Quantity + b.Quantity,
		Times:    a.Times + b.Times,
	}
	return result, nil
}

func (s *DropMatrixService) convertDropMatrixElementsToDropMatrixQueryResult(ctx context.Context, dropMatrixElements []*models.DropMatrixElement) (*models.DropMatrixQueryResult, error) {
	dropMatrixQueryResult := &models.DropMatrixQueryResult{
		Matrix: make([]*models.OneDropMatrixElement, 0),
	}
	var groupedResults []linq.Group
	linq.From(dropMatrixElements).
		GroupByT(
			func(el *models.DropMatrixElement) int { return el.RangeID },
			func(el *models.DropMatrixElement) *models.DropMatrixElement { return el },
		).
		ToSlice(&groupedResults)
	for _, group := range groupedResults {
		rangeId := group.Key.(int)
		var timeRange *models.TimeRange
		if rangeId == 0 {
			timeRange = group.Group[0].(*models.DropMatrixElement).TimeRange
		} else {
			tr, err := s.TimeRangeService.GetTimeRangeById(ctx, rangeId)
			if err != nil {
				return nil, err
			}
			timeRange = tr
		}

		for _, el := range group.Group {
			dropMatrixElement := el.(*models.DropMatrixElement)
			dropMatrixQueryResult.Matrix = append(dropMatrixQueryResult.Matrix, &models.OneDropMatrixElement{
				StageID:   dropMatrixElement.StageID,
				ItemID:    dropMatrixElement.ItemID,
				Quantity:  dropMatrixElement.Quantity,
				Times:     dropMatrixElement.Times,
				TimeRange: timeRange,
			})
		}
	}
	return dropMatrixQueryResult, nil
}

func (s *DropMatrixService) applyShimForDropMatrixQuery(ctx context.Context, server string, showClosedZones bool, stageFilterStr, itemFilterStr string, queryResult *models.DropMatrixQueryResult) (*modelv2.DropMatrixQueryResult, error) {
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
		linq.From(currentDropInfos).SelectT(func(el *models.DropInfo) int { return el.StageID }).Distinct().ToSlice(&openingStageIds)
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
			StartTime: el.TimeRange.StartTime.UnixMilli(),
			EndTime:   endTime,
		}
		if oneDropMatrixElement.EndTime.Int64 == constants.FakeEndTimeMilli {
			oneDropMatrixElement.EndTime = null.NewInt(0, false)
		}
		results.Matrix = append(results.Matrix, &oneDropMatrixElement)
	}
	return results, nil
}
