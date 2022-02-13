package service

import (
	"sync"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/utils"
)

type TrendService struct {
	TimeRangeService            *TimeRangeService
	DropReportService           *DropReportService
	DropInfoService             *DropInfoService
	PatternMatrixElementService *PatternMatrixElementService
	DropPatternElementService   *DropPatternElementService
	TrendElementService         *TrendElementService
	StageService                *StageService
	ItemService                 *ItemService
}

func NewTrendService(
	timeRangeService *TimeRangeService,
	dropReportService *DropReportService,
	dropInfoService *DropInfoService,
	patternMatrixElementService *PatternMatrixElementService,
	dropPatternElementService *DropPatternElementService,
	trendElementService *TrendElementService,
	stageService *StageService,
	itemService *ItemService,
) *TrendService {
	return &TrendService{
		TimeRangeService:            timeRangeService,
		DropReportService:           dropReportService,
		DropInfoService:             dropInfoService,
		PatternMatrixElementService: patternMatrixElementService,
		DropPatternElementService:   dropPatternElementService,
		TrendElementService:         trendElementService,
		StageService:                stageService,
		ItemService:                 itemService,
	}
}

// Cache: shimSavedTrendResults#server:{server}, 24hrs
func (s *TrendService) GetShimSavedTrendResults(ctx *fiber.Ctx, server string) (*shims.TrendQueryResult, error) {
	var shimResult shims.TrendQueryResult
	err := cache.ShimSavedTrendResults.Get(server, &shimResult)
	if err == nil {
		return &shimResult, nil
	}

	queryResult, err := s.getSavedTrendResults(ctx, server)
	if err != nil {
		return nil, err
	}
	slowShimResult, err := s.applyShimForTrendQuery(ctx, queryResult)
	if err != nil {
		return nil, err
	}
	go cache.ShimSavedTrendResults.Set(server, slowShimResult, 24*time.Hour)
	return slowShimResult, nil
}

func (s *TrendService) GetShimCustomizedTrendResults(ctx *fiber.Ctx, server string, startTime *time.Time, intervalLength_hrs int, intervalNum int, stageIds []int, itemIds []int, accountId *null.Int) (*shims.TrendQueryResult, error) {
	trendQueryResult, err := s.QueryTrend(ctx, server, startTime, intervalLength_hrs, intervalNum, stageIds, itemIds, accountId)
	if err != nil {
		return nil, err
	}
	return s.applyShimForTrendQuery(ctx, trendQueryResult)
}

func (s *TrendService) QueryTrend(
	ctx *fiber.Ctx, server string, startTime *time.Time, intervalLength_hrs int, intervalNum int, stageIdFilter []int, itemIdFilter []int, accountId *null.Int,
) (*models.TrendQueryResult, error) {
	trendElements, err := s.calcTrend(ctx, server, startTime, intervalLength_hrs, intervalNum, stageIdFilter, itemIdFilter, accountId)
	if err != nil {
		return nil, err
	}
	return s.convertTrendElementsToTrendQueryResult(trendElements)
}

func (s *TrendService) RefreshTrendElements(ctx *fiber.Ctx, server string) error {
	maxAccumulableTimeRanges, err := s.TimeRangeService.GetMaxAccumulableTimeRangesByServer(ctx, server)
	if err != nil {
		return err
	}

	toCalc := make([]map[string]interface{}, 0)
	for stageId, maxAccumulableTimeRangesForOneStage := range maxAccumulableTimeRanges {
		itemIdsMapByTimeRange := make(map[string][]int, 0)
		for itemId, timeRanges := range maxAccumulableTimeRangesForOneStage {
			sortedTimeRanges := make([]*models.TimeRange, 0)
			linq.From(timeRanges).SortT(func(a, b *models.TimeRange) bool {
				return a.StartTime.Before(*b.StartTime)
			}).ToSlice(&sortedTimeRanges)
			combinedTimeRange := models.TimeRange{
				StartTime: sortedTimeRanges[0].StartTime,
				EndTime:   sortedTimeRanges[len(sortedTimeRanges)-1].EndTime,
			}
			if _, ok := itemIdsMapByTimeRange[combinedTimeRange.String()]; !ok {
				itemIdsMapByTimeRange[combinedTimeRange.String()] = make([]int, 0)
			}
			itemIdsMapByTimeRange[combinedTimeRange.String()] = append(itemIdsMapByTimeRange[combinedTimeRange.String()], itemId)
		}
		for rangeStr, itemIds := range itemIdsMapByTimeRange {
			var timeRange *models.TimeRange
			timeRange = timeRange.FromString(rangeStr)
			startTime := *timeRange.StartTime
			endTime := *timeRange.EndTime
			if endTime.After(time.Now()) {
				endTime = time.Now()
			}

			diff := int(endTime.Sub(startTime).Hours())
			intervalNum := diff / 24
			if diff%24 != 0 {
				intervalNum++
			}

			if intervalNum > 60 {
				intervalNum = 60
				startTime = endTime.Add(time.Hour * time.Duration((-1)*24*(intervalNum+1)))
			}

			toCalc = append(toCalc, map[string]interface{}{
				"stageId":     stageId,
				"itemIds":     itemIds,
				"startTime":   startTime,
				"intervalNum": intervalNum,
			})
		}
	}

	toSave := []*models.TrendElement{}
	ch := make(chan []*models.TrendElement, 15)
	var wg sync.WaitGroup

	go func() {
		for {
			m := <-ch
			toSave = append(toSave, m...)
			wg.Done()
		}
	}()

	limiter := make(chan struct{}, 7)
	wg.Add(len(toCalc))
	for _, el := range toCalc {
		limiter <- struct{}{}
		go func(el map[string]interface{}) {
			startTime := el["startTime"].(time.Time)
			intervalNum := el["intervalNum"].(int)
			stageId := el["stageId"].(int)
			itemIds := el["itemIds"].([]int)
			currentBatch, err := s.calcTrend(ctx, server, &startTime, 24, intervalNum, []int{stageId}, itemIds, &null.Int{})
			if err != nil {
				return
			}
			ch <- currentBatch
			<-limiter
		}(el)
	}
	wg.Wait()

	log.Debug().Msgf("toSave length: %v", len(toSave))
	return s.TrendElementService.BatchSaveElements(ctx, toSave, server)
}

func (s *TrendService) getSavedTrendResults(ctx *fiber.Ctx, server string) (*models.TrendQueryResult, error) {
	trendElements, err := s.TrendElementService.GetElementsByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	return s.convertTrendElementsToTrendQueryResult(trendElements)
}

func (s *TrendService) calcTrend(
	ctx *fiber.Ctx, server string, startTime *time.Time, intervalLength_hrs int, intervalNum int, stageIdFilter []int, itemIdFilter []int, accountId *null.Int,
) ([]*models.TrendElement, error) {
	endTime := startTime.Add(time.Hour * time.Duration(intervalLength_hrs*intervalNum))
	timeRange := models.TimeRange{
		StartTime: startTime,
		EndTime:   &endTime,
	}
	dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, []*models.TimeRange{&timeRange}, stageIdFilter, itemIdFilter)
	if err != nil {
		return nil, err
	}

	quantityResults, err := s.DropReportService.CalcTotalQuantityForTrend(ctx, server, startTime, intervalLength_hrs, intervalNum, utils.GetStageIdItemIdMapFromDropInfos(dropInfos), accountId)
	if err != nil {
		return nil, err
	}
	timesResults, err := s.DropReportService.CalcTotalTimesForTrend(ctx, server, startTime, intervalLength_hrs, intervalNum, utils.GetStageIdsFromDropInfos(dropInfos), accountId)
	if err != nil {
		return nil, err
	}
	combinedResults := s.combineQuantityAndTimesResults(quantityResults, timesResults)

	finalResults := make([]*models.TrendElement, 0)
	linq.From(combinedResults).
		SelectT(func(el *models.CombinedResultForTrend) *models.TrendElement {
			return &models.TrendElement{
				StageID:   el.StageID,
				ItemID:    el.ItemID,
				Quantity:  el.Quantity,
				Times:     el.Times,
				Server:    server,
				StartTime: el.StartTime,
				EndTime:   el.EndTime,
				GroupID:   el.GroupID,
			}
		}).
		ToSlice(&finalResults)
	return finalResults, nil
}

func (s *TrendService) combineQuantityAndTimesResults(
	quantityResults []*models.TotalQuantityResultForTrend, timesResults []*models.TotalTimesResultForTrend,
) []*models.CombinedResultForTrend {
	var firstGroupResultsForQuantity []linq.Group
	combinedResults := make([]*models.CombinedResultForTrend, 0)
	linq.From(quantityResults).
		GroupByT(
			func(result *models.TotalQuantityResultForTrend) int { return result.GroupID },
			func(result *models.TotalQuantityResultForTrend) *models.TotalQuantityResultForTrend { return result }).
		ToSlice(&firstGroupResultsForQuantity)
	groupResultsMap := make(map[int]map[int]map[int]int)
	for _, firstGroupElements := range firstGroupResultsForQuantity {
		groupId := firstGroupElements.Key.(int)
		var secondGroupResultsForQuantity []linq.Group
		linq.From(firstGroupElements.Group).
			GroupByT(
				func(result *models.TotalQuantityResultForTrend) int { return result.StageID },
				func(result *models.TotalQuantityResultForTrend) *models.TotalQuantityResultForTrend { return result }).
			ToSlice(&secondGroupResultsForQuantity)
		quantityResultsMap := make(map[int]map[int]int)
		for _, secondGroupElements := range secondGroupResultsForQuantity {
			stageId := secondGroupElements.Key.(int)
			resultsMap := make(map[int]int, 0)
			linq.From(secondGroupElements.Group).
				ToMapByT(&resultsMap,
					func(el interface{}) int { return el.(*models.TotalQuantityResultForTrend).ItemID },
					func(el interface{}) int { return el.(*models.TotalQuantityResultForTrend).TotalQuantity })
			quantityResultsMap[stageId] = resultsMap
		}
		groupResultsMap[groupId] = quantityResultsMap
	}

	var firstGroupResultsForTimes []linq.Group
	linq.From(timesResults).
		GroupByT(
			func(result *models.TotalTimesResultForTrend) int { return result.GroupID },
			func(result *models.TotalTimesResultForTrend) *models.TotalTimesResultForTrend { return result }).
		ToSlice(&firstGroupResultsForTimes)
	for _, firstGroupElements := range firstGroupResultsForTimes {
		groupId := firstGroupElements.Key.(int)
		quantityResultsMapForOneGroup := groupResultsMap[groupId]
		var secondGroupResultsForTimes []linq.Group
		linq.From(firstGroupElements.Group).
			GroupByT(
				func(result *models.TotalTimesResultForTrend) int { return result.StageID },
				func(result *models.TotalTimesResultForTrend) *models.TotalTimesResultForTrend { return result }).
			ToSlice(&secondGroupResultsForTimes)
		for _, secondGroupElements := range secondGroupResultsForTimes {
			stageId := secondGroupElements.Key.(int)
			resultsMap := quantityResultsMapForOneGroup[stageId]
			for _, el := range secondGroupElements.Group {
				times := el.(*models.TotalTimesResultForTrend).TotalTimes
				for itemId, quantity := range resultsMap {
					startTime := el.(*models.TotalTimesResultForTrend).IntervalStart
					endTime := el.(*models.TotalTimesResultForTrend).IntervalEnd
					combinedResults = append(combinedResults, &models.CombinedResultForTrend{
						GroupID:   groupId,
						StageID:   stageId,
						ItemID:    itemId,
						Quantity:  quantity,
						Times:     times,
						StartTime: startTime,
						EndTime:   endTime,
					})
				}
			}
		}
	}
	return combinedResults
}

func (s *TrendService) convertTrendElementsToTrendQueryResult(trendElements []*models.TrendElement) (*models.TrendQueryResult, error) {
	var groupedResults []linq.Group
	linq.From(trendElements).
		GroupByT(
			func(el *models.TrendElement) int { return el.StageID },
			func(el *models.TrendElement) *models.TrendElement { return el },
		).
		ToSlice(&groupedResults)
	trendQueryResult := &models.TrendQueryResult{
		Trends: make([]*models.StageTrend, 0),
	}
	for _, el := range groupedResults {
		stageId := el.Key.(int)
		stageTrend := &models.StageTrend{
			StageID: stageId,
			Results: make([]*models.ItemTrend, 0),
		}
		var groupedResults2 []linq.Group
		linq.From(el.Group).
			GroupByT(
				func(el *models.TrendElement) int { return el.ItemID },
				func(el *models.TrendElement) *models.TrendElement { return el },
			).
			ToSlice(&groupedResults2)
		var startTime *time.Time
		for _, el2 := range groupedResults2 {
			itemId := el2.Key.(int)
			var sortedElements []*models.TrendElement
			linq.From(el2.Group).
				SortT(func(el1, el2 *models.TrendElement) bool { return el1.GroupID < el2.GroupID }).
				ToSlice(&sortedElements)
			startTime = sortedElements[0].StartTime
			maxGroupId := linq.From(sortedElements).SelectT(func(el *models.TrendElement) int { return el.GroupID }).Max().(int)
			timesArray := make([]int, maxGroupId+1)
			quantityArray := make([]int, maxGroupId+1)
			for _, el3 := range sortedElements {
				timesArray[el3.GroupID] = el3.Times
				quantityArray[el3.GroupID] = el3.Quantity
			}
			stageTrend.Results = append(stageTrend.Results, &models.ItemTrend{
				ItemID:    itemId,
				Times:     timesArray,
				Quantity:  quantityArray,
				StartTime: startTime,
			})
		}
		trendQueryResult.Trends = append(trendQueryResult.Trends, stageTrend)
	}
	return trendQueryResult, nil
}

func (s *TrendService) applyShimForTrendQuery(ctx *fiber.Ctx, queryResult *models.TrendQueryResult) (*shims.TrendQueryResult, error) {
	results := &shims.TrendQueryResult{
		Trend: make(map[string]*shims.StageTrend),
	}
	for _, stageTrend := range queryResult.Trends {
		stage, err := s.StageService.GetStageById(ctx, stageTrend.StageID)
		if err != nil {
			return nil, err
		}
		shimStageTrend := shims.StageTrend{
			Results: make(map[string]*shims.OneItemTrend),
		}
		for _, itemTrend := range stageTrend.Results {
			item, err := s.ItemService.GetItemById(ctx, itemTrend.ItemID)
			if err != nil {
				return nil, err
			}
			shimStageTrend.Results[item.ArkItemID] = &shims.OneItemTrend{
				Quantity:  itemTrend.Quantity,
				Times:     itemTrend.Times,
				StartTime: itemTrend.StartTime.UnixMilli(),
			}
		}
		results.Trend[stage.ArkStageID] = &shimStageTrend
	}
	return results, nil
}
