package service

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/rs/zerolog/log"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	modelv2 "github.com/penguin-statistics/backend-next/internal/models/v2"
	"github.com/penguin-statistics/backend-next/internal/util"
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

// Cache: shimSavedTrendResults#server:{server}, 24hrs, records last modified time
func (s *TrendService) GetShimSavedTrendResults(ctx context.Context, server string) (*modelv2.TrendQueryResult, error) {
	valueFunc := func() (*modelv2.TrendQueryResult, error) {
		queryResult, err := s.getSavedTrendResults(ctx, server)
		if err != nil {
			return nil, err
		}
		slowShimResult, err := s.applyShimForSavedTrendQuery(ctx, server, queryResult)
		if err != nil {
			return nil, err
		}
		return slowShimResult, nil
	}

	var shimResult modelv2.TrendQueryResult
	calculated, err := cache.ShimSavedTrendResults.MutexGetSet(server, &shimResult, valueFunc, 24*time.Hour)
	if err != nil {
		return nil, err
	} else if calculated {
		cache.LastModifiedTime.Set("[shimSavedTrendResults#server:"+server+"]", time.Now(), 0)
	}
	return &shimResult, nil
}

func (s *TrendService) GetShimCustomizedTrendResults(ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIds []int, itemIds []int, accountId null.Int) (*modelv2.TrendQueryResult, error) {
	trendQueryResult, err := s.QueryTrend(ctx, server, startTime, intervalLength, intervalNum, stageIds, itemIds, accountId)
	if err != nil {
		return nil, err
	}
	return s.applyShimForCustomizedTrendQuery(ctx, trendQueryResult, startTime)
}

func (s *TrendService) QueryTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIdFilter []int, itemIdFilter []int, accountId null.Int,
) (*models.TrendQueryResult, error) {
	trendElements, err := s.calcTrend(ctx, server, startTime, intervalLength, intervalNum, stageIdFilter, itemIdFilter, accountId)
	if err != nil {
		return nil, err
	}
	return s.convertTrendElementsToTrendQueryResult(trendElements)
}

func (s *TrendService) RefreshTrendElements(ctx context.Context, server string) error {
	maxAccumulableTimeRanges, err := s.TimeRangeService.GetMaxAccumulableTimeRangesByServer(ctx, server)
	if err != nil {
		return err
	}

	calcq := make([]map[string]any, 0)
	for stageId, maxAccumulableTimeRangesForOneStage := range maxAccumulableTimeRanges {
		itemIdsMapByTimeRange := make(map[string][]int)
		for itemId, timeRanges := range maxAccumulableTimeRangesForOneStage {
			sortedTimeRanges := make([]*models.TimeRange, 0)

			linq.From(timeRanges).
				SortT(func(a, b *models.TimeRange) bool {
					return a.StartTime.Before(*b.StartTime)
				}).
				ToSlice(&sortedTimeRanges)

			combinedTimeRange := models.TimeRange{
				StartTime: sortedTimeRanges[0].StartTime,
				EndTime:   sortedTimeRanges[len(sortedTimeRanges)-1].EndTime,
			}

			combinedTimeRangeKey := combinedTimeRange.String()
			if _, ok := itemIdsMapByTimeRange[combinedTimeRangeKey]; !ok {
				itemIdsMapByTimeRange[combinedTimeRangeKey] = make([]int, 0)
			}

			itemIdsMapByTimeRange[combinedTimeRangeKey] = append(itemIdsMapByTimeRange[combinedTimeRangeKey], itemId)
		}
		for rangeStr, itemIds := range itemIdsMapByTimeRange {
			timeRange := models.TimeRangeFromString(rangeStr)
			startTime := *timeRange.StartTime
			endTime := *timeRange.EndTime

			if endTime.After(time.Now()) {
				endTime = time.Now()
			}

			startTime = util.GetGameDayStartTime(server, startTime)
			if !util.IsGameDayStartTime(server, endTime) {
				endTime = util.GetGameDayEndTime(server, endTime)
			} else {
				loc := constants.LocMap[server]
				endTime = endTime.In(loc)
			}

			diff := int(endTime.Sub(startTime).Hours())
			intervalNum := diff / 24
			if diff%24 != 0 { // shouldn't happen actually
				intervalNum++
			}

			if intervalNum > constants.DefaultIntervalNum {
				intervalNum = constants.DefaultIntervalNum
				startTime = endTime.Add(time.Hour * time.Duration((-1)*24*intervalNum))
			}

			calcq = append(calcq, map[string]any{
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
			m, ok := <-ch
			if !ok {
				return
			}
			toSave = append(toSave, m...)
			wg.Done()
		}
	}()

	limiter := make(chan struct{}, runtime.NumCPU())
	wg.Add(len(calcq))
	for _, el := range calcq {
		limiter <- struct{}{}
		go func(el map[string]any) {
			startTime := el["startTime"].(time.Time)
			intervalNum := el["intervalNum"].(int)
			stageId := el["stageId"].(int)
			itemIds := el["itemIds"].([]int)
			currentBatch, err := s.calcTrend(ctx, server, &startTime, time.Hour*24, intervalNum, []int{stageId}, itemIds, null.NewInt(0, false))
			if err != nil {
				return
			}
			ch <- currentBatch
			<-limiter
		}(el)
	}
	wg.Wait()
	close(ch)

	if err := s.TrendElementService.BatchSaveElements(ctx, toSave, server); err != nil {
		return err
	}
	return cache.ShimSavedTrendResults.Delete(server)
}

func (s *TrendService) getSavedTrendResults(ctx context.Context, server string) (*models.TrendQueryResult, error) {
	trendElements, err := s.TrendElementService.GetElementsByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	return s.convertTrendElementsToTrendQueryResult(trendElements)
}

func (s *TrendService) calcTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIdFilter []int, itemIdFilter []int, accountId null.Int,
) ([]*models.TrendElement, error) {
	endTime := startTime.Add(time.Hour * time.Duration(int(intervalLength.Hours())*intervalNum))
	if e := log.Trace(); e.Enabled() {
		e.Str("server", server).
			Time("startTime", *startTime).
			Time("endTime", endTime).
			Dur("intervalLength", intervalLength).
			Int("intervalNum", intervalNum).
			Msg("calculating trend...")
	}
	timeRange := models.TimeRange{
		StartTime: startTime,
		EndTime:   &endTime,
	}
	dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, []*models.TimeRange{&timeRange}, stageIdFilter, itemIdFilter)
	if err != nil {
		return nil, err
	}

	quantityResults, err := s.DropReportService.CalcTotalQuantityForTrend(ctx, server, startTime, intervalLength, intervalNum, util.GetStageIdItemIdMapFromDropInfos(dropInfos), accountId)
	if err != nil {
		return nil, err
	}
	timesResults, err := s.DropReportService.CalcTotalTimesForTrend(ctx, server, startTime, intervalLength, intervalNum, util.GetStageIdsFromDropInfos(dropInfos), accountId)
	if err != nil {
		return nil, err
	}
	combinedResults := s.combineQuantityAndTimesResults(quantityResults, timesResults)

	finalResults := make([]*models.TrendElement, 0, len(combinedResults))
	for _, result := range combinedResults {
		finalResults = append(finalResults, &models.TrendElement{
			StageID:   result.StageID,
			ItemID:    result.ItemID,
			Quantity:  result.Quantity,
			Times:     result.Times,
			Server:    server,
			StartTime: result.StartTime,
			EndTime:   result.EndTime,
			GroupID:   result.GroupID,
		})
	}
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
			resultsMap := make(map[int]int)
			linq.From(secondGroupElements.Group).
				ToMapByT(&resultsMap,
					func(el any) int { return el.(*models.TotalQuantityResultForTrend).ItemID },
					func(el any) int { return el.(*models.TotalQuantityResultForTrend).TotalQuantity })
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
				startTime := el.(*models.TotalTimesResultForTrend).IntervalStart
				endTime := el.(*models.TotalTimesResultForTrend).IntervalEnd
				for itemId, quantity := range resultsMap {
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
			minGroupId := linq.From(sortedElements).SelectT(func(el *models.TrendElement) int { return el.GroupID }).Min().(int)
			maxGroupId := linq.From(sortedElements).SelectT(func(el *models.TrendElement) int { return el.GroupID }).Max().(int)
			timesArray := make([]int, maxGroupId+1)
			quantityArray := make([]int, maxGroupId+1)
			for _, el3 := range sortedElements {
				timesArray[el3.GroupID] = el3.Times
				quantityArray[el3.GroupID] = el3.Quantity
			}
			stageTrend.Results = append(stageTrend.Results, &models.ItemTrend{
				ItemID:     itemId,
				Times:      timesArray,
				Quantity:   quantityArray,
				StartTime:  startTime,
				MinGroupID: minGroupId,
				MaxGroupID: maxGroupId,
			})
		}
		trendQueryResult.Trends = append(trendQueryResult.Trends, stageTrend)
	}
	return trendQueryResult, nil
}

func (s *TrendService) applyShimForCustomizedTrendQuery(ctx context.Context, queryResult *models.TrendQueryResult, startTime *time.Time) (*modelv2.TrendQueryResult, error) {
	itemsMapById, err := s.ItemService.GetItemsMapById(ctx)
	if err != nil {
		return nil, err
	}

	stagesMapById, err := s.StageService.GetStagesMapById(ctx)
	if err != nil {
		return nil, err
	}

	results := &modelv2.TrendQueryResult{
		Trend: make(map[string]*modelv2.StageTrend),
	}
	for _, stageTrend := range queryResult.Trends {
		stage := stagesMapById[stageTrend.StageID]
		shimStageTrend := modelv2.StageTrend{
			Results:   make(map[string]*modelv2.OneItemTrend),
			StartTime: startTime.UnixMilli(),
		}

		for _, itemTrend := range stageTrend.Results {
			item := itemsMapById[itemTrend.ItemID]
			shimStageTrend.Results[item.ArkItemID] = &modelv2.OneItemTrend{
				Quantity: itemTrend.Quantity,
				Times:    itemTrend.Times,
			}
		}
		if len(shimStageTrend.Results) > 0 {
			results.Trend[stage.ArkStageID] = &shimStageTrend
		}
	}
	return results, nil
}

func (s *TrendService) applyShimForSavedTrendQuery(ctx context.Context, server string, queryResult *models.TrendQueryResult) (*modelv2.TrendQueryResult, error) {
	shimMinStartTime := util.GetGameDayEndTime(server, time.Now()).Add(-1 * constants.DefaultIntervalNum * 24 * time.Hour)
	currentGameDayEndTime := util.GetGameDayEndTime(server, time.Now())

	itemsMapById, err := s.ItemService.GetItemsMapById(ctx)
	if err != nil {
		return nil, err
	}

	stagesMapById, err := s.StageService.GetStagesMapById(ctx)
	if err != nil {
		return nil, err
	}

	results := &modelv2.TrendQueryResult{
		Trend: make(map[string]*modelv2.StageTrend),
	}
	for _, stageTrend := range queryResult.Trends {
		stage := stagesMapById[stageTrend.StageID]
		shimStageTrend := modelv2.StageTrend{
			Results: make(map[string]*modelv2.OneItemTrend),
		}
		var stageTrendStartTime *time.Time

		// calc stage trend start time
		for _, itemTrend := range stageTrend.Results {
			itemStartTime := itemTrend.StartTime.Add((-1) * time.Duration(itemTrend.MinGroupID) * 24 * time.Hour)
			// if the end time of this item is before the global trend start time (now - 60d), then we don't show it
			dayNum := len(itemTrend.Quantity)
			itemEndTime := itemStartTime.Add(time.Duration(dayNum) * 24 * time.Hour)
			if itemEndTime.Before(shimMinStartTime) {
				continue
			}
			// adjust stage trend start time
			if stageTrendStartTime == nil || !stageTrendStartTime.Equal(shimMinStartTime) && itemStartTime.Before(*stageTrendStartTime) {
				if !itemTrend.StartTime.After(shimMinStartTime) {
					stageTrendStartTime = &shimMinStartTime
				} else {
					stageTrendStartTime = &itemStartTime
				}
			}
		}

		for _, itemTrend := range stageTrend.Results {
			item := itemsMapById[itemTrend.ItemID]

			itemStartTime := itemTrend.StartTime.Add((-1) * time.Duration(itemTrend.MinGroupID) * 24 * time.Hour)
			dayNum := len(itemTrend.Quantity)
			itemEndTime := itemStartTime.Add(time.Duration(dayNum) * 24 * time.Hour)
			if itemEndTime.Before(shimMinStartTime) {
				continue
			}

			// add 0s to the head of quantity and times arrays according to itemStartTime
			headZeroNum := int(itemStartTime.Sub(*stageTrendStartTime).Hours() / 24)
			if headZeroNum > 0 {
				itemTrend.Quantity = append(make([]int, headZeroNum), itemTrend.Quantity...)
				itemTrend.Times = append(make([]int, headZeroNum), itemTrend.Times...)
			}

			// add 0s to the tail of quantity and times arrays according to itemEndTime
			tailZeroNum := int(currentGameDayEndTime.Sub(itemEndTime).Hours() / 24)
			if tailZeroNum > 0 {
				itemTrend.Quantity = append(itemTrend.Quantity, make([]int, tailZeroNum)...)
				itemTrend.Times = append(itemTrend.Times, make([]int, tailZeroNum)...)
			}

			shimStageTrend.Results[item.ArkItemID] = &modelv2.OneItemTrend{
				Quantity: itemTrend.Quantity,
				Times:    itemTrend.Times,
			}
		}
		if len(shimStageTrend.Results) > 0 {
			shimStageTrend.StartTime = stageTrendStartTime.UnixMilli()
			results.Trend[stage.ArkStageID] = &shimStageTrend
		}
	}
	return results, nil
}
