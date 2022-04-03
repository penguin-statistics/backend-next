package service

import (
	"context"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	modelsv2 "github.com/penguin-statistics/backend-next/internal/models/v2"
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

// Cache: shimLatestPatternMatrixResults#server:{server}, 24hrs, records last modified time
func (s *PatternMatrixService) GetShimLatestPatternMatrixResults(ctx context.Context, server string, accountId null.Int) (*modelsv2.PatternMatrixQueryResult, error) {
	valueFunc := func() (*modelsv2.PatternMatrixQueryResult, error) {
		queryResult, err := s.getLatestPatternMatrixResults(ctx, server, accountId)
		if err != nil {
			return nil, err
		}
		slowResults, err := s.applyShimForPatternMatrixQuery(ctx, queryResult)
		if err != nil {
			return nil, err
		}
		return slowResults, nil
	}

	var results modelsv2.PatternMatrixQueryResult
	if !accountId.Valid {
		calculated, err := cache.ShimLatestPatternMatrixResults.MutexGetSet(server, &results, valueFunc, 24*time.Hour)
		if err != nil {
			return nil, err
		} else if calculated {
			cache.LastModifiedTime.Set("[shimLatestPatternMatrixResults#server:"+server+"]", time.Now(), 0)
		}
		return &results, nil
	} else {
		return valueFunc()
	}
}

func (s *PatternMatrixService) RefreshAllPatternMatrixElements(ctx context.Context, server string) error {
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
			m, ok := <-ch
			if !ok {
				return
			}
			toSave = append(toSave, m...)
			wg.Done()
		}
	}()

	limiter := make(chan struct{}, runtime.NumCPU())
	wg.Add(len(stageIdsMap))

	errCount := int32(0)

	for rangeId, stageIds := range stageIdsMap {
		limiter <- struct{}{}
		go func(rangeId int, stageIds []int) {
			defer func() {
				<-limiter
			}()

			timeRanges := []*models.TimeRange{timeRangesMap[rangeId]}
			currentBatch, err := s.calcPatternMatrixForTimeRanges(ctx, server, timeRanges, stageIds, null.NewInt(0, false))
			if err != nil {
				log.Error().Err(err).Msg("failed to calculate pattern matrix")
				atomic.AddInt32(&errCount, 1)
				return
			}

			ch <- currentBatch
		}(rangeId, stageIds)
	}
	wg.Wait()
	close(ch)

	if errCount > 0 {
		return errors.New("failed to calculate pattern matrix (total: " + strconv.Itoa(int(errCount)) + " errors); see log for details")
	}

	if err := s.PatternMatrixElementService.BatchSaveElements(ctx, toSave, server); err != nil {
		return err
	}
	return cache.ShimLatestPatternMatrixResults.Delete(server)
}

func (s *PatternMatrixService) getLatestPatternMatrixResults(ctx context.Context, server string, accountId null.Int) (*models.PatternMatrixQueryResult, error) {
	patternMatrixElements, err := s.getLatestPatternMatrixElements(ctx, server, accountId)
	if err != nil {
		return nil, err
	}
	return s.convertPatternMatrixElementsToDropPatternQueryResult(ctx, server, patternMatrixElements)
}

func (s *PatternMatrixService) getLatestPatternMatrixElements(ctx context.Context, server string, accountId null.Int) ([]*models.PatternMatrixElement, error) {
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
	ctx context.Context, server string, timeRanges []*models.TimeRange, stageIdFilter []int, accountId null.Int,
) ([]*models.PatternMatrixElement, error) {
	results := make([]*models.PatternMatrixElement, 0)

	dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, timeRanges, stageIdFilter, nil)
	if err != nil {
		return nil, err
	}

	stageIds := utils.GetStageIdsFromDropInfos(dropInfos)

	// exclude gacha box stages
	gachaboxStages, err := s.StageService.GetGachaBoxStages(ctx)
	if err != nil {
		return nil, err
	}
	excludeStageIdsSet := make(map[int]struct{}, len(gachaboxStages))
	for _, stage := range gachaboxStages {
		excludeStageIdsSet[stage.StageID] = struct{}{}
	}
	linq.From(stageIds).WhereT(func(stageId int) bool {
		_, ok := excludeStageIdsSet[stageId]
		return !ok
	}).ToSlice(&stageIds)
	if len(stageIds) == 0 {
		return results, nil
	}

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
		resultsMap := make(map[int]int)
		linq.From(firstGroupElements.Group).
			ToMapByT(&resultsMap,
				func(el any) int { return el.(*models.TotalQuantityResultForPatternMatrix).PatternID },
				func(el any) int { return el.(*models.TotalQuantityResultForPatternMatrix).TotalQuantity })
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
	ctx context.Context, server string, patternMatrixElements []*models.PatternMatrixElement,
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

func (s *PatternMatrixService) applyShimForPatternMatrixQuery(ctx context.Context, queryResult *models.PatternMatrixQueryResult) (*modelsv2.PatternMatrixQueryResult, error) {
	results := &modelsv2.PatternMatrixQueryResult{
		PatternMatrix: make([]*modelsv2.OnePatternMatrixElement, 0),
	}

	itemsMapById, err := s.ItemService.GetItemsMapById(ctx)
	if err != nil {
		return nil, err
	}

	stagesMapById, err := s.StageService.GetStagesMapById(ctx)
	if err != nil {
		return nil, err
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
			stage := stagesMapById[oneDropPattern.StageID]
			endTime := null.NewInt(oneDropPattern.TimeRange.EndTime.UnixMilli(), true)
			dropPatternElements, err := s.DropPatternElementService.GetDropPatternElementsByPatternId(ctx, patternId)
			if err != nil {
				return nil, err
			}
			// create pattern object from dropPatternElements
			pattern := modelsv2.Pattern{
				Drops: make([]*modelsv2.OneDrop, 0),
			}
			linq.From(dropPatternElements).SortT(func(el1, el2 *models.DropPatternElement) bool {
				item1 := itemsMapById[el1.ItemID]
				item2 := itemsMapById[el2.ItemID]
				return item1.SortID < item2.SortID
			}).ToSlice(&dropPatternElements)
			for _, dropPatternElement := range dropPatternElements {
				item := itemsMapById[dropPatternElement.ItemID]
				pattern.Drops = append(pattern.Drops, &modelsv2.OneDrop{
					ItemID:   item.ArkItemID,
					Quantity: dropPatternElement.Quantity,
				})
			}
			onePatternMatrixElement := modelsv2.OnePatternMatrixElement{
				StageID:   stage.ArkStageID,
				Times:     oneDropPattern.Times,
				Quantity:  oneDropPattern.Quantity,
				StartTime: oneDropPattern.TimeRange.StartTime.UnixMilli(),
				EndTime:   endTime,
				Pattern:   &pattern,
			}
			if onePatternMatrixElement.EndTime.Int64 == constants.FakeEndTimeMilli {
				onePatternMatrixElement.EndTime = null.NewInt(0, false)
			}
			results.PatternMatrix = append(results.PatternMatrix, &onePatternMatrixElement)
		}
	}
	return results, nil
}
