package service

import (
	"fmt"
	"sync"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/models"
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

func (s *DropMatrixService) RefreshAllDropMatrixElements(ctx *fiber.Ctx, server string) error {
	toSave := []models.DropMatrixElement{}
	allTimeRanges, err := s.TimeRangeService.GetAllTimeRangesByServer(ctx, server)
	if err != nil {
		return err
	}
	ch := make(chan []models.DropMatrixElement, 15)
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
			results, err := s.calcDropMatrixForTimeRanges(ctx, server, timeRanges, nil, nil, &null.Int{})
			if err != nil {
				return
			}

			stageTimesMap := map[int]int{} // save stage times for later use

			// grouping results by stage id
			var groupedResults []linq.Group
			linq.From(results).
				GroupByT(
					func(el map[string]interface{}) int { return el["stageId"].(int) },
					func(el map[string]interface{}) map[string]interface{} { return el }).ToSlice(&groupedResults)

			currentBatch := []models.DropMatrixElement{}
			for _, el := range groupedResults {
				stageId := el.Key.(int)

				// get all item ids which are dropped in this stage and in this time range
				dropItemIds, _ := s.DropInfoService.GetItemDropSetByStageIdAndRangeId(ctx, server, stageId, timeRange.RangeID)

				// use a fake hashset to save item ids
				dropSet := make(map[int]struct{}, len(dropItemIds))
				for _, itemId := range dropItemIds {
					dropSet[itemId] = struct{}{}
				}

				for _, el2 := range el.Group {
					itemId := el2.(map[string]interface{})["itemId"].(int)
					quantity := el2.(map[string]interface{})["quantity"].(int)
					times := el2.(map[string]interface{})["times"].(int)
					dropMatrixElement := models.DropMatrixElement{
						StageID:  stageId,
						ItemID:   itemId,
						RangeID:  timeRange.RangeID,
						Quantity: quantity,
						Times:    times,
						Server:   server,
					}
					currentBatch = append(currentBatch, dropMatrixElement)
					delete(dropSet, itemId)        // remove existing item ids from drop set
					stageTimesMap[stageId] = times // record stage times into a map
				}
				// add those items which do not show up in the matrix (quantity is 0)
				for itemId := range dropSet {
					dropMatrixElementWithZeroQuantity := models.DropMatrixElement{
						StageID:  stageId,
						ItemID:   itemId,
						RangeID:  timeRange.RangeID,
						Quantity: 0,
						Times:    stageTimesMap[stageId],
						Server:   server,
					}
					currentBatch = append(currentBatch, dropMatrixElementWithZeroQuantity)
				}
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

func (s *DropMatrixService) calcDropMatrixForTimeRanges(
	ctx *fiber.Ctx, queryServer string, timeRanges []*models.TimeRange, stageIdFilter []int, itemIdFilter []int, accountId *null.Int) ([]map[string]interface{}, error) {
	dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(ctx, queryServer, timeRanges, stageIdFilter, itemIdFilter)
	if err != nil {
		return nil, err
	}

	results := make([]map[string]interface{}, 0)
	for _, timeRange := range timeRanges {
		quantityResults, err := s.DropReportService.CalcTotalQuantity(ctx, queryServer, timeRange, getStageIdItemIdMapFromDropInfos(dropInfos), accountId)
		if err != nil {
			return nil, err
		}
		timesResults, err := s.DropReportService.CalcTotalTimes(ctx, queryServer, timeRange, getStageIdsFromDropInfos(dropInfos), accountId)
		if err != nil {
			return nil, err
		}
		combinedResults := combineQuantityAndTimesResults(quantityResults, timesResults)
		for _, result := range combinedResults {
			result["timeRange"] = timeRange
			results = append(results, result)
		}
	}
	return results, nil
}

func combineQuantityAndTimesResults(quantityResults []map[string]interface{}, timesResults []map[string]interface{}) []map[string]interface{} {
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

func getStageIdItemIdMapFromDropInfos(dropInfos []*models.DropInfo) map[int][]int {
	stageIdItemIdMap := make(map[int][]int)
	var groupedResults []linq.Group
	linq.From(dropInfos).
		WhereT(func (dropInfo *models.DropInfo) bool { return dropInfo.ItemID.Valid }).
		GroupByT(
			func (dropInfo *models.DropInfo) int { return dropInfo.StageID }, 
			func (dropInfo *models.DropInfo) int { return int(dropInfo.ItemID.Int64) },
		).ToSlice(&groupedResults)
	for _, groupedResult := range groupedResults {
		stageId := groupedResult.Key.(int)
		itemIds := make([]int, 0)
		linq.From(groupedResult.Group).Distinct().ToSlice(&itemIds)
		if len(itemIds) > 0 {
			stageIdItemIdMap[stageId] = itemIds
		}
	}
	return stageIdItemIdMap
}

func getStageIdsFromDropInfos(dropInfos []*models.DropInfo) []int {
	stageIds := make([]int, 0)
	linq.From(dropInfos).SelectT(func (dropInfo *models.DropInfo) int { return dropInfo.StageID }).Distinct().ToSlice(&stageIds)
	return stageIds
}
