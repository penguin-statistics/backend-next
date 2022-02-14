package service

import (
	"sync"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/utils"
)

type PatternMatrixService struct {
	TimeRangeService            *TimeRangeService
	DropReportService           *DropReportService
	DropInfoService             *DropInfoService
	PatternMatrixElementService *PatternMatrixElementService
	DropPatternElementService   *DropPatternElementService
	StageService                *StageService
	ItemService                 *ItemService
}

func NewPatternMatrixService(
	timeRangeService *TimeRangeService,
	dropReportService *DropReportService,
	dropInfoService *DropInfoService,
	patternMatrixElementService *PatternMatrixElementService,
	dropPatternElementService *DropPatternElementService,
	stageService *StageService,
	itemService *ItemService,
) *PatternMatrixService {
	return &PatternMatrixService{
		TimeRangeService:            timeRangeService,
		DropReportService:           dropReportService,
		DropInfoService:             dropInfoService,
		PatternMatrixElementService: patternMatrixElementService,
		DropPatternElementService:   dropPatternElementService,
		StageService:                stageService,
		ItemService:                 itemService,
	}
}

// Cache: shimLatestPatternMatrixResults#server:{server}, 24hrs
func (s *PatternMatrixService) GetShimLatestPatternMatrixResults(ctx *fiber.Ctx, server string, accountId *null.Int) (*shims.PatternMatrixQueryResult, error) {
	valueFunc := func() (interface{}, error) {
		queryResult, err := s.getLatestPatternMatrixResults(ctx, server, accountId)
		if err != nil {
			return nil, err
		}
		slowResults, err := s.applyShimForPatternMatrixQuery(ctx, queryResult)
		if err != nil {
			return nil, err
		}
		return *slowResults, nil
	}

	var results shims.PatternMatrixQueryResult
	if !accountId.Valid {
		calculated, err := cache.ShimLatestPatternMatrixResults.MutexGetSet(server, &results, valueFunc, 24*time.Hour)
		if err != nil {
			return nil, err
		} else if calculated {
			cache.LastModifiedTime.Set("[shimLatestPatternMatrixResults#server:"+server+"]", time.Now(), 0)
		}
		return &results, nil
	} else {
		r, err := valueFunc()
		if err != nil {
			return nil, err
		}
		results = r.(shims.PatternMatrixQueryResult)
		return &results, nil
	}
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

	var usedTimeMap sync.Map

	limiter := make(chan struct{}, 7)
	wg.Add(len(stageIdsMap))

	for rangeId, stageIds := range stageIdsMap {
		limiter <- struct{}{}
		go func(rangeId int, stageIds []int) {
			startTime := time.Now()

			timeRanges := []*models.TimeRange{timeRangesMap[rangeId]}
			currentBatch, err := s.calcPatternMatrixForTimeRanges(ctx, server, timeRanges, stageIds, &null.Int{})
			if err != nil {
				return
			}

			ch <- currentBatch
			<-limiter

			usedTimeMap.Store(rangeId, int(time.Since(startTime).Microseconds()))
		}(rangeId, stageIds)
	}

	wg.Wait()

	log.Debug().Msgf("toSave length: %v", len(toSave))

	if err := s.PatternMatrixElementService.BatchSaveElements(ctx, toSave, server); err != nil {
		return err
	}
	return cache.ShimLatestPatternMatrixResults.Delete(server)
}

func (s *PatternMatrixService) getLatestPatternMatrixResults(ctx *fiber.Ctx, server string, accountId *null.Int) (*models.PatternMatrixQueryResult, error) {
	patternMatrixElements, err := s.getLatestPatternMatrixElements(ctx, server, accountId)
	if err != nil {
		return nil, err
	}
	return s.convertPatternMatrixElementsToDropPatternQueryResult(ctx, server, patternMatrixElements)
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
) (*models.PatternMatrixQueryResult, error) {
	timeRangesMap, err := s.TimeRangeService.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}

	result := &models.PatternMatrixQueryResult{
		PatternMatrix: make([]*models.OnePatternMatrixElement, 0),
	}
	for _, patternMatrixElement := range patternMatrixElements {
		timeRange := timeRangesMap[patternMatrixElement.RangeID]
		result.PatternMatrix = append(result.PatternMatrix, &models.OnePatternMatrixElement{
			StageID:   patternMatrixElement.StageID,
			PatternID: patternMatrixElement.PatternID,
			Quantity:  patternMatrixElement.Quantity,
			Times:     patternMatrixElement.Times,
			TimeRange: timeRange,
		})
	}
	return result, nil
}

func (s *PatternMatrixService) applyShimForPatternMatrixQuery(ctx *fiber.Ctx, queryResult *models.PatternMatrixQueryResult) (*shims.PatternMatrixQueryResult, error) {
	results := &shims.PatternMatrixQueryResult{
		PatternMatrix: make([]*shims.OnePatternMatrixElement, 0),
	}
	var groupedResults []linq.Group
	linq.From(queryResult.PatternMatrix).
		GroupByT(
			func(el *models.OnePatternMatrixElement) int { return el.PatternID },
			func(el *models.OnePatternMatrixElement) *models.OnePatternMatrixElement { return el },
		).ToSlice(&groupedResults)
	for _, group := range groupedResults {
		patternId := group.Key.(int)
		for _, el := range group.Group {
			oneDropPattern := el.(*models.OnePatternMatrixElement)
			stage, err := s.StageService.GetStageById(ctx, oneDropPattern.StageID)
			if err != nil {
				return nil, err
			}
			endTime := null.NewInt(oneDropPattern.TimeRange.EndTime.UnixMilli(), true)
			dropPatternElements, err := s.DropPatternElementService.GetDropPatternElementsByPatternId(ctx, patternId)
			if err != nil {
				return nil, err
			}
			// create pattern object from dropPatternElements
			pattern := shims.Pattern{
				Drops: make([]*shims.OneDrop, 0),
			}
			for _, dropPatternElement := range dropPatternElements {
				item, err := s.ItemService.GetItemById(ctx, dropPatternElement.ItemID)
				if err != nil {
					return nil, err
				}
				pattern.Drops = append(pattern.Drops, &shims.OneDrop{
					ItemID:   item.ArkItemID,
					Quantity: dropPatternElement.Quantity,
				})
			}
			onePatternMatrixElement := shims.OnePatternMatrixElement{
				StageID:   stage.ArkStageID,
				Times:     oneDropPattern.Times,
				Quantity:  oneDropPattern.Quantity,
				StartTime: oneDropPattern.TimeRange.StartTime.UnixMilli(),
				EndTime:   &endTime,
				Pattern:   &pattern,
			}
			if onePatternMatrixElement.EndTime.Int64 == constants.FakeEndTimeMilli {
				onePatternMatrixElement.EndTime = nil
			}
			results.PatternMatrix = append(results.PatternMatrix, &onePatternMatrixElement)
		}
	}
	return results, nil
}
