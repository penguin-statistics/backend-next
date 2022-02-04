package service

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/utils"
)

type DropMatrixService struct {
	TimeRangeService         *TimeRangeService
	DropReportService        *DropReportService
	DropInfoService          *DropInfoService
	DropMatrixElementService *DropMatrixElementService
}

func NewDropMatrixService(
	timeRangeService *TimeRangeService, 
	dropReportService *DropReportService, 
	dropInfoService *DropInfoService,
	dropMatrixElementService *DropMatrixElementService,
) *DropMatrixService {
	return &DropMatrixService{
		TimeRangeService: timeRangeService,
		DropReportService: dropReportService,
		DropInfoService: dropInfoService,
		DropMatrixElementService: dropMatrixElementService,
	}
}

func (s *DropMatrixService) GetMaxAccumulableDropMatrixResults(ctx *fiber.Ctx, server string, accountId *null.Int) ([]map[string]interface{}, error) {
	dropMatrixElements, err := s.getDropMatrixElements(ctx, server, accountId)
	if err != nil {
		return nil, err
	}
	elementsMap := utils.GetDropMatrixElementsMap(dropMatrixElements)
	return s.generateMaxAccumulableResultsFromDropMatrixElementsMap(ctx, server, elementsMap)	
}

func (s *DropMatrixService) RefreshAllDropMatrixElements(ctx *fiber.Ctx, server string) error {
	toSave := []*models.DropMatrixElement{}
	allTimeRanges, err := s.TimeRangeService.GetAllTimeRangesByServer(ctx, server)
	if err != nil {
		return err
	}
	ch := make(chan []*models.DropMatrixElement, 15)
	var wg sync.WaitGroup

	go func() {
		for {
			m := <-ch
			toSave = append(toSave, m...)
			wg.Done()
		}
	}()

	usedTimeMap := sync.Map{}

	limiter := make(chan struct{}, 7)
	wg.Add(len(allTimeRanges))
	for _, timeRange := range allTimeRanges {
		limiter <- struct{}{}
		go func(timeRange *models.TimeRange) {
			fmt.Println("<   :", timeRange.RangeID)
			startTime := time.Now()

			timeRanges := []*models.TimeRange{timeRange}
			currentBatch, err := s.CalcDropMatrixForTimeRanges(ctx, server, timeRanges, nil, nil, &null.Int{})
			if err != nil {
				return
			}

			ch <- currentBatch
			<-limiter

			usedTimeMap.Store(timeRange.RangeID, int(time.Since(startTime).Microseconds()))
			fmt.Println("   > :", timeRange.RangeID, "@", time.Since(startTime))
		}(timeRange)
	}

	wg.Wait()

	log.Debug().Msgf("toSave length: %v", len(toSave))
	return s.DropMatrixElementService.BatchSaveElements(ctx, toSave, server)
}

func (s *DropMatrixService) getDropMatrixElements(ctx *fiber.Ctx, server string, accountId *null.Int) ([]*models.DropMatrixElement, error){
	if accountId.Valid {
		maxAccumulableTimeRanges, err := s.TimeRangeService.GetMaxAccumulableTimeRangesByServer(ctx, server)
		if err != nil {
			return nil, err
		}
		timeRanges := make([]*models.TimeRange, 0)

		timeRangesMap := make(map[int]*models.TimeRange, 0)
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
		return s.CalcDropMatrixForTimeRanges(ctx, server, timeRanges, nil, nil, accountId)
	} else {
		return s.DropMatrixElementService.GetElementsByServer(ctx, server)
	}
}

func (s *DropMatrixService) CalcDropMatrixForTimeRanges(
	ctx *fiber.Ctx, server string, timeRanges []*models.TimeRange, stageIdFilter []int, itemIdFilter []int, accountId *null.Int) ([]*models.DropMatrixElement, error) {
	dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, timeRanges, stageIdFilter, itemIdFilter)
	if err != nil {
		return nil, err
	}

	results := make([]map[string]interface{}, 0)
	for _, timeRange := range timeRanges {
		quantityResults, err := s.DropReportService.CalcTotalQuantityForDropMatrix(ctx, server, timeRange, utils.GetStageIdItemIdMapFromDropInfos(dropInfos), accountId)
		if err != nil {
			return nil, err
		}
		timesResults, err := s.DropReportService.CalcTotalTimesForDropMatrix(ctx, server, timeRange, utils.GetStageIdsFromDropInfos(dropInfos), accountId)
		if err != nil {
			return nil, err
		}
		combinedResults := s.combineQuantityAndTimesResults(quantityResults, timesResults)
		for _, result := range combinedResults {
			result["timeRange"] = timeRange
			results = append(results, result)
		}
	}

	// save stage times for later use
	stageTimesMap := map[int]int{}

	// grouping results by stage id
	var groupedResults []linq.Group
	linq.From(results).
		GroupByT(
			func(el map[string]interface{}) int { return el["stageId"].(int) },
			func(el map[string]interface{}) map[string]interface{} { return el }).ToSlice(&groupedResults)

	dropMatrixElements := make([]*models.DropMatrixElement, 0)
	for _, el := range groupedResults {
		stageId := el.Key.(int)
		var groupedResults2 []linq.Group
		linq.From(el.Group).
			GroupByT(
				func (el map[string]interface{}) int { return el["timeRange"].(*models.TimeRange).RangeID }, 
				func (el map[string]interface{}) map[string]interface{} { return el }).
			ToSlice(&groupedResults2)
		for _, el2 := range groupedResults2 {
			rangeId := el2.Key.(int)
			// get all item ids which are dropped in this stage and in this time range
			dropItemIds, _ := s.DropInfoService.GetItemDropSetByStageIdAndRangeId(ctx, server, stageId, rangeId)

			// use a fake hashset to save item ids
			dropSet := make(map[int]struct{}, len(dropItemIds))
			for _, itemId := range dropItemIds {
				dropSet[itemId] = struct{}{}
			}	
			for _, el3 := range el2.Group {
				itemId := el3.(map[string]interface{})["itemId"].(int)
				quantity := el3.(map[string]interface{})["quantity"].(int)
				times := el3.(map[string]interface{})["times"].(int)
				dropMatrixElement := models.DropMatrixElement{
					StageID:  stageId,
					ItemID:   itemId,
					RangeID:  rangeId,
					Quantity: quantity,
					Times:    times,
					Server:   server,
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
				dropMatrixElements = append(dropMatrixElements, &dropMatrixElementWithZeroQuantity)
			}
		}
	}
	return dropMatrixElements, nil
}

func (s *DropMatrixService) combineQuantityAndTimesResults(quantityResults []map[string]interface{}, timesResults []map[string]interface{}) []map[string]interface{} {
	var firstGroupResults []linq.Group
	combinedResults := make([]map[string]interface{}, 0)
	linq.From(quantityResults).
		GroupByT(
			func(result map[string]interface{}) interface{} { return result["stage_id"] },
			func(result map[string]interface{}) map[string]interface{} { return result }).
		ToSlice(&firstGroupResults)
	quantityResultsMap := make(map[int]map[int]int)
	for _, firstGroupElements := range firstGroupResults {
		stageId := int(firstGroupElements.Key.(int64))
		resultsMap := make(map[int]int, 0)
		linq.From(firstGroupElements.Group).
			ToMapByT(&resultsMap,
				func(el interface{}) int { return int(el.(map[string]interface{})["item_id"].(int64)) },
				func(el interface{}) int { return int(el.(map[string]interface{})["total_quantity"].(int64)) })
		quantityResultsMap[stageId] = resultsMap
	}

	var secondGroupResults []linq.Group
	linq.From(timesResults).
		GroupByT(
			func(result map[string]interface{}) interface{} { return result["stage_id"] },
			func(result map[string]interface{}) map[string]interface{} { return result }).
		ToSlice(&secondGroupResults)
	for _, secondGroupResults := range secondGroupResults {
		stageId := int(secondGroupResults.Key.(int64))
		quantityResultsMapForOneStage := quantityResultsMap[stageId]
		for _, el := range secondGroupResults.Group {
			times := int(el.(map[string]interface{})["total_times"].(int64))
			for itemId, quantity := range quantityResultsMapForOneStage {
				combinedResults = append(combinedResults, map[string]interface{}{
					"stageId":  stageId,
					"itemId":   itemId,
					"times":    times,
					"quantity": quantity,
				})
			}
		}
	}
	return combinedResults
}

func (s *DropMatrixService) generateMaxAccumulableResultsFromDropMatrixElementsMap(ctx *fiber.Ctx, server string, elementsMap map[int] map[int] map[int] *models.DropMatrixElement) ([]map[string]interface{}, error) {
	results := make([]map[string]interface{}, 0)
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
			var combinedDropMatrixResult map[string]interface{}
			combinedDropMatrixResult = nil
			for _, timeRange := range timeRanges {
				element, ok := subMapByRangeId[timeRange.RangeID]
				if !ok {
					continue
				}
				oneElementResult := map[string]interface{}{
					"stageId": stageId,
					"itemId": itemId,
					"quantity": element.Quantity,
					"times": element.Times,
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
					if (err != nil) {
						return nil, err
					}
				}
			}
			if combinedDropMatrixResult != nil {
				combinedDropMatrixResult["start"] = startTime.UnixMilli()
				combinedDropMatrixResult["end"] = endTime.UnixMilli()
				results = append(results, combinedDropMatrixResult)	
			}
		}
	}
	return results, nil	
}

func (s *DropMatrixService) combineDropMatrixResults(a map[string]interface{}, b map[string]interface{}) (map[string]interface{}, error) {
	if (a["stageId"] != b["stageId"]) {
		return nil, errors.New("stageId not match")
	}
	if (a["itemId"] != b["itemId"]) {
		return nil, errors.New("itemId not match")
	}
	result := make(map[string]interface{}, 0)
	result["stageId"] = a["stageId"]
	result["itemId"] = a["itemId"]
	result["times"] = a["times"].(int) + b["times"].(int)
	result["quantity"] = a["quantity"].(int) + b["quantity"].(int)
	return result, nil
}
