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
	"github.com/penguin-statistics/backend-next/internal/utils"
)

type PatternMatrixService struct {
	TimeRangeService            *TimeRangeService
	DropReportService           *DropReportService
	DropInfoService             *DropInfoService
	PatternMatrixElementService *PatternMatrixElementService
	DropPatternElementService   *DropPatternElementService
}

func NewPatternMatrixService(
	timeRangeService *TimeRangeService, 
	dropReportService *DropReportService, 
	dropInfoService *DropInfoService,
	patternMatrixElementService *PatternMatrixElementService,
	dropPatternElementService *DropPatternElementService,
) *PatternMatrixService {
	return &PatternMatrixService{
		TimeRangeService: timeRangeService,
		DropReportService: dropReportService,
		DropInfoService: dropInfoService,
		PatternMatrixElementService: patternMatrixElementService,
		DropPatternElementService: dropPatternElementService,
	}
}

func (s *PatternMatrixService) GetLatestPatternMatrixResults(ctx *fiber.Ctx, server string, accountId *null.Int) ([]map[string]interface{}, error) {
	patternMatrixElements, err := s.getLatestPatternMatrixElements(ctx, server, accountId)
	if err != nil {
		return nil, err
	}
	return s.generateLatestResultsFromPatternMatrixElements(ctx, server, patternMatrixElements)
}

func (s *PatternMatrixService) RefreshAllPatternMatrixElements(ctx *fiber.Ctx, server string) error {
	toSave := []*models.PatternMatrixElement{}
	timeRangesMap, err := s.TimeRangeService.GetTimeRangesMap(ctx, server)
	if err != nil {
		return err
	}
	allTimeRanges, err := s.TimeRangeService.GetLatestTimeRangesByServer(ctx, server)
	if err != nil {
		return err
	}
	stageIdsMap := s.getStageIdsMapByTimeRange(allTimeRanges)
	ch := make(chan []*models.PatternMatrixElement, 15)
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
	wg.Add(len(stageIdsMap))
	for rangeId, stageIds := range stageIdsMap {
		limiter <- struct{}{}
		go func(rangeId int, stageIds []int) {
			fmt.Println("<   :", rangeId)
			startTime := time.Now()

			timeRanges := []*models.TimeRange{timeRangesMap[rangeId]}
			currentBatch, err := s.calcPatternMatrixForTimeRanges(ctx, server, timeRanges, stageIds, &null.Int{})
			if err != nil {
				return
			}

			ch <- currentBatch
			<-limiter

			usedTimeMap.Store(rangeId, int(time.Since(startTime).Microseconds()))
			fmt.Println("   > :", rangeId, "@", time.Since(startTime))
		}(rangeId, stageIds)
	}

	wg.Wait()

	log.Debug().Msgf("toSave length: %v", len(toSave))
	return s.PatternMatrixElementService.BatchSaveElements(ctx, toSave, server)
}

func (s *PatternMatrixService) getLatestPatternMatrixElements(ctx *fiber.Ctx, server string, accountId *null.Int) ([]*models.PatternMatrixElement, error){
	if accountId.Valid {
		timeRangesMap, err := s.TimeRangeService.GetTimeRangesMap(ctx, server)
		if err != nil {
			return nil, err
		}
		allTimeRanges, err := s.TimeRangeService.GetLatestTimeRangesByServer(ctx, server)
		if err != nil {
			return nil, err
		}
		stageIdsMap := s.getStageIdsMapByTimeRange(allTimeRanges)
		elements := make([]*models.PatternMatrixElement, 0)
		for rangeId, stageIds := range stageIdsMap {
			timeRanges := []*models.TimeRange{timeRangesMap[rangeId]}
			currentBatch, err := s.calcPatternMatrixForTimeRanges(ctx, server, timeRanges, stageIds, accountId)
			if err != nil {
				return nil, err
			}
			elements = append(elements, currentBatch...)
		}
		return elements, nil
	} else {
		return s.PatternMatrixElementService.GetElementsByServer(ctx, server)
	}
}

func (s *PatternMatrixService) calcPatternMatrixForTimeRanges(
	ctx *fiber.Ctx, server string, timeRanges []*models.TimeRange, stageIdFilter []int, accountId *null.Int) ([]*models.PatternMatrixElement, error) {
	dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, timeRanges, stageIdFilter, nil)
	if err != nil {
		return nil, err
	}

	stageIds := utils.GetStageIdsFromDropInfos(dropInfos)
	results := make([]*models.PatternMatrixElement, 0)
	for _, timeRange := range timeRanges {
		quantityResults, err := s.DropReportService.CalcTotalQuantityForPatternMatrix(ctx, server, timeRange, stageIds, accountId)
		if err != nil {
			return nil, err
		}
		timesResults, err := s.DropReportService.CalcTotalTimesForPatternMatrix(ctx, server, timeRange, stageIds, accountId)
		if err != nil {
			return nil, err
		}
		combinedResults := s.combineQuantityAndTimesResults(quantityResults, timesResults)
		for _, result := range combinedResults {
			results = append(results, &models.PatternMatrixElement{
				StageID: result["stageId"].(int),
				PatternID: result["patternId"].(int),
				RangeID: timeRange.RangeID,
				Quantity: result["quantity"].(int),
				Times: result["times"].(int),
				Server: server,
			})
		}
	}
	return results, nil
}

func (s *PatternMatrixService) combineQuantityAndTimesResults(quantityResults []map[string]interface{}, timesResults []map[string]interface{}) []map[string]interface{} {
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
				func(el interface{}) int { return int(el.(map[string]interface{})["pattern_id"].(int64)) },
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
			for patternId, quantity := range quantityResultsMapForOneStage {
				combinedResults = append(combinedResults, map[string]interface{}{
					"stageId":  stageId,
					"patternId": patternId,
					"times":    times,
					"quantity": quantity,
				})
			}
		}
	}
	return combinedResults
}

func (s *PatternMatrixService) getStageIdsMapByTimeRange(timeRangesMap map[int]*models.TimeRange) map[int] []int {
	results := make(map[int] []int)
	for stageId, timeRange := range timeRangesMap {
		if _, ok := results[timeRange.RangeID]; !ok {
			results[timeRange.RangeID] = make([]int, 0)
		}
		results[timeRange.RangeID] = append(results[timeRange.RangeID], stageId)
	}
	return results
}

func (s *PatternMatrixService) generateLatestResultsFromPatternMatrixElements(ctx *fiber.Ctx, server string, patternMatrixElements []*models.PatternMatrixElement) ([]map[string]interface{}, error) {
	timeRangesMap, err := s.TimeRangeService.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}
	patternIds := make([]int, 0)
	linq.From(patternMatrixElements).SelectT(func (el *models.PatternMatrixElement) int { return el.PatternID }).Distinct().ToSlice(&patternIds)
	dropPatternElementsMap, err := s.DropPatternElementService.GetDropPatternElementsMapByPatternIds(ctx, patternIds)
	if err != nil {
		return nil, err
	}

	results := make([]map[string]interface{}, 0)
	var groupedResults []linq.Group
	linq.From(patternMatrixElements).
		GroupByT(
			func (el *models.PatternMatrixElement) int { return el.PatternID }, 
			func (el *models.PatternMatrixElement) *models.PatternMatrixElement { return el },
		).
		ToSlice(&groupedResults)
	for _, el := range groupedResults {
		patternId := el.Key.(int)
		resultsForOnePatternId := make([]map[string]interface{}, 0)
		linq.From(el.Group).
			SelectT(func (el2 interface{}) map[string]interface{} {
				patternMatrixElement := el2.(*models.PatternMatrixElement)
				timeRange := timeRangesMap[patternMatrixElement.RangeID]
				dropPatternElements := dropPatternElementsMap[patternId]
				dropsMaps := make([]map[string]interface{}, 0)
				linq.From(dropPatternElements).
					SelectT(func (dropPatternElement *models.DropPatternElement) map[string]interface{} { 
						return map[string]interface{}{
							"itemId": dropPatternElement.ItemID,
							"quantity": dropPatternElement.Quantity,
						}
					}).
					ToSlice(&dropsMaps)
				patternValue := map[string]interface{}{
					"drops": dropsMaps,
				}
				return map[string]interface{}{
					"stageId": patternMatrixElement.StageID,
					"quantity": patternMatrixElement.Quantity,
					"times": patternMatrixElement.Times,
					"start": timeRange.StartTime.UnixMilli(),
					"end": timeRange.EndTime.UnixMilli(),
					"pattern": patternValue,
				}
			}).
			ToSlice(&resultsForOnePatternId)
		results = append(results, resultsForOnePatternId...)
	}
	return results, nil
}
