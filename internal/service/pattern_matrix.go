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
		TimeRangeService:            timeRangeService,
		DropReportService:           dropReportService,
		DropInfoService:             dropInfoService,
		PatternMatrixElementService: patternMatrixElementService,
		DropPatternElementService:   dropPatternElementService,
	}
}

func (s *PatternMatrixService) GetSavedPatternMatrixResults(ctx *fiber.Ctx, server string, accountId *null.Int) (*models.DropPatternQueryResult, error) {
	patternMatrixElements, err := s.getLatestPatternMatrixElements(ctx, server, accountId)
	if err != nil {
		return nil, err
	}
	return s.convertPatternMatrixElementsToDropPatternQueryResult(ctx, server, patternMatrixElements)
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

func (s *PatternMatrixService) getLatestPatternMatrixElements(ctx *fiber.Ctx, server string, accountId *null.Int) ([]*models.PatternMatrixElement, error) {
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
	ctx *fiber.Ctx, server string, timeRanges []*models.TimeRange, stageIdFilter []int, accountId *null.Int,
) ([]*models.PatternMatrixElement, error) {
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
				StageID:   result.StageID,
				PatternID: result.PatternID,
				RangeID:   timeRange.RangeID,
				Quantity:  result.Quantity,
				Times:     result.Times,
				Server:    server,
			})
		}
	}
	return results, nil
}

func (s *PatternMatrixService) combineQuantityAndTimesResults(
	quantityResults []*models.TotalQuantityResultForPatternMatrix, timesResults []*models.TotalTimesResult,
) []*models.CombinedResultForDropPattern {
	var firstGroupResults []linq.Group
	combinedResults := make([]*models.CombinedResultForDropPattern, 0)
	linq.From(quantityResults).
		GroupByT(
			func(result *models.TotalQuantityResultForPatternMatrix) int { return result.StageID },
			func(result *models.TotalQuantityResultForPatternMatrix) *models.TotalQuantityResultForPatternMatrix {
				return result
			}).
		ToSlice(&firstGroupResults)
	quantityResultsMap := make(map[int]map[int]int)
	for _, firstGroupElements := range firstGroupResults {
		stageId := firstGroupElements.Key.(int)
		resultsMap := make(map[int]int, 0)
		linq.From(firstGroupElements.Group).
			ToMapByT(&resultsMap,
				func(el interface{}) int { return el.(*models.TotalQuantityResultForPatternMatrix).PatternID },
				func(el interface{}) int { return el.(*models.TotalQuantityResultForPatternMatrix).TotalQuantity })
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
			for patternId, quantity := range quantityResultsMapForOneStage {
				combinedResults = append(combinedResults, &models.CombinedResultForDropPattern{
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

func (s *PatternMatrixService) getStageIdsMapByTimeRange(timeRangesMap map[int]*models.TimeRange) map[int][]int {
	results := make(map[int][]int)
	for stageId, timeRange := range timeRangesMap {
		if _, ok := results[timeRange.RangeID]; !ok {
			results[timeRange.RangeID] = make([]int, 0)
		}
		results[timeRange.RangeID] = append(results[timeRange.RangeID], stageId)
	}
	return results
}

func (s *PatternMatrixService) convertPatternMatrixElementsToDropPatternQueryResult(
	ctx *fiber.Ctx, server string, patternMatrixElements []*models.PatternMatrixElement,
) (*models.DropPatternQueryResult, error) {
	timeRangesMap, err := s.TimeRangeService.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}

	result := &models.DropPatternQueryResult{
		DropPatterns: make([]*models.OneDropPattern, 0),
	}
	for _, patternMatrixElement := range patternMatrixElements {
		timeRange := timeRangesMap[patternMatrixElement.RangeID]
		result.DropPatterns = append(result.DropPatterns, &models.OneDropPattern{
			StageID:   patternMatrixElement.StageID,
			PatternID: patternMatrixElement.PatternID,
			Quantity:  patternMatrixElement.Quantity,
			Times:     patternMatrixElement.Times,
			TimeRange: timeRange,
		})
	}
	return result, nil
}
