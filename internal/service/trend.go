package service

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/rs/zerolog/log"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
	"github.com/penguin-statistics/backend-next/internal/pkg/gameday"
	"github.com/penguin-statistics/backend-next/internal/util"
)

type Trend struct {
	TimeRangeService            *TimeRange
	DropReportService           *DropReport
	DropInfoService             *DropInfo
	PatternMatrixElementService *PatternMatrixElement
	DropPatternElementService   *DropPatternElement
	TrendElementService         *TrendElement
	StageService                *Stage
	ItemService                 *Item
}

func NewTrend(
	timeRangeService *TimeRange,
	dropReportService *DropReport,
	dropInfoService *DropInfo,
	patternMatrixElementService *PatternMatrixElement,
	dropPatternElementService *DropPatternElement,
	trendElementService *TrendElement,
	stageService *Stage,
	itemService *Item,
) *Trend {
	return &Trend{
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
func (s *Trend) GetShimSavedTrendResults(ctx context.Context, server string) (*modelv2.TrendQueryResult, error) {
	valueFunc := func() (*modelv2.TrendQueryResult, error) {
		queryResult, err := s.getSavedTrendResults(ctx, server, constant.SourceCategoryAll)
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

func (s *Trend) GetShimCustomizedTrendResults(
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIds []int, itemIds []int, accountId null.Int,
) (*modelv2.TrendQueryResult, error) {
	trendQueryResult, err := s.QueryTrend(ctx, server, startTime, intervalLength, intervalNum, stageIds, itemIds, accountId, constant.SourceCategoryAll)
	if err != nil {
		return nil, err
	}
	return s.applyShimForCustomizedTrendQuery(ctx, trendQueryResult, startTime)
}

func (s *Trend) QueryTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIdFilter []int, itemIdFilter []int, accountId null.Int, sourceCategory string,
) (*model.TrendQueryResult, error) {
	trendElements, err := s.calcTrend(ctx, server, startTime, intervalLength, intervalNum, stageIdFilter, itemIdFilter, accountId, sourceCategory)
	if err != nil {
		return nil, err
	}
	return s.convertTrendElementsToTrendQueryResult(trendElements)
}

func (s *Trend) RefreshTrendElements(ctx context.Context, server string) error {
	maxAccumulableTimeRanges, err := s.TimeRangeService.GetMaxAccumulableTimeRangesByServer(ctx, server)
	if err != nil {
		return err
	}

	calcq := make([]map[string]any, 0)
	for stageId, maxAccumulableTimeRangesForOneStage := range maxAccumulableTimeRanges {
		itemIdsMapByTimeRange := make(map[string][]int)
		for itemId, timeRanges := range maxAccumulableTimeRangesForOneStage {
			sortedTimeRanges := make([]*model.TimeRange, 0)

			linq.From(timeRanges).
				SortT(func(a, b *model.TimeRange) bool {
					return a.StartTime.Before(*b.StartTime)
				}).
				ToSlice(&sortedTimeRanges)

			combinedTimeRange := model.TimeRange{
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
			timeRange := model.TimeRangeFromString(rangeStr)
			startTime := *timeRange.StartTime
			endTime := *timeRange.EndTime

			if endTime.After(time.Now()) {
				endTime = time.Now()
			}

			startTime = gameday.StartTime(server, startTime)
			if !gameday.IsStartTime(server, endTime) {
				endTime = gameday.EndTime(server, endTime)
			} else {
				loc := constant.LocMap[server]
				endTime = endTime.In(loc)
			}

			diff := int(endTime.Sub(startTime).Hours())
			intervalNum := diff / 24
			if diff%24 != 0 { // shouldn't happen actually
				intervalNum++
			}

			if intervalNum > constant.DefaultIntervalNum {
				intervalNum = constant.DefaultIntervalNum
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

	toSave := []*model.TrendElement{}
	ch := make(chan []*model.TrendElement, 15)
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
			manualResults, err := s.calcTrend(ctx, server, &startTime, time.Hour*24, intervalNum, []int{stageId}, itemIds, null.NewInt(0, false), constant.SourceCategoryManual)
			if err != nil {
				return
			}
			automatedResults, err := s.calcTrend(ctx, server, &startTime, time.Hour*24, intervalNum, []int{stageId}, itemIds, null.NewInt(0, false), constant.SourceCategoryAutomated)
			if err != nil {
				return
			}
			allResults, err := s.calcTrend(ctx, server, &startTime, time.Hour*24, intervalNum, []int{stageId}, itemIds, null.NewInt(0, false), constant.SourceCategoryAll)
			if err != nil {
				return
			}
			currentBatch := append(manualResults, automatedResults...)
			currentBatch = append(currentBatch, allResults...)
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

func (s *Trend) getSavedTrendResults(ctx context.Context, server string, sourceCategory string) (*model.TrendQueryResult, error) {
	trendElements, err := s.TrendElementService.GetElementsByServerAndSourceCategory(ctx, server, sourceCategory)
	if err != nil {
		return nil, err
	}
	return s.convertTrendElementsToTrendQueryResult(trendElements)
}

func (s *Trend) calcTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIdFilter []int, itemIdFilter []int, accountId null.Int, sourceCategory string,
) ([]*model.TrendElement, error) {
	endTime := startTime.Add(time.Hour * time.Duration(int(intervalLength.Hours())*intervalNum))
	if e := log.Trace(); e.Enabled() {
		e.Str("server", server).
			Time("startTime", *startTime).
			Time("endTime", endTime).
			Dur("intervalLength", intervalLength).
			Int("intervalNum", intervalNum).
			Msg("calculating trend...")
	}
	timeRange := model.TimeRange{
		StartTime: startTime,
		EndTime:   &endTime,
	}
	dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, []*model.TimeRange{&timeRange}, stageIdFilter, itemIdFilter)
	if err != nil {
		return nil, err
	}

	quantityResults, err := s.DropReportService.CalcTotalQuantityForTrend(ctx, server, startTime, intervalLength, intervalNum, util.GetStageIdItemIdMapFromDropInfos(dropInfos), accountId, sourceCategory)
	if err != nil {
		return nil, err
	}
	timesResults, err := s.DropReportService.CalcTotalTimesForTrend(ctx, server, startTime, intervalLength, intervalNum, util.GetStageIdsFromDropInfos(dropInfos), accountId, sourceCategory)
	if err != nil {
		return nil, err
	}
	combinedResults, err := s.combineQuantityAndTimesResults(ctx, server, itemIdFilter, quantityResults, timesResults)
	if err != nil {
		return nil, err
	}

	finalResults := make([]*model.TrendElement, 0, len(combinedResults))
	for _, result := range combinedResults {
		finalResults = append(finalResults, &model.TrendElement{
			StageID:        result.StageID,
			ItemID:         result.ItemID,
			Quantity:       result.Quantity,
			Times:          result.Times,
			Server:         server,
			StartTime:      result.StartTime,
			EndTime:        result.EndTime,
			GroupID:        result.GroupID,
			SourceCategory: sourceCategory,
		})
	}
	return finalResults, nil
}

func (s *Trend) combineQuantityAndTimesResults(
	ctx context.Context, server string, itemIdFilter []int, quantityResults []*model.TotalQuantityResultForTrend, timesResults []*model.TotalTimesResultForTrend,
) ([]*model.CombinedResultForTrend, error) {
	var firstGroupResultsForQuantity []linq.Group
	combinedResults := make([]*model.CombinedResultForTrend, 0)
	linq.From(quantityResults).
		GroupByT(
			func(result *model.TotalQuantityResultForTrend) int { return result.GroupID },
			func(result *model.TotalQuantityResultForTrend) *model.TotalQuantityResultForTrend { return result }).
		ToSlice(&firstGroupResultsForQuantity)
	groupResultsMap := make(map[int]map[int]map[int]int)
	for _, firstGroupElements := range firstGroupResultsForQuantity {
		groupId := firstGroupElements.Key.(int)
		var secondGroupResultsForQuantity []linq.Group
		linq.From(firstGroupElements.Group).
			GroupByT(
				func(result *model.TotalQuantityResultForTrend) int { return result.StageID },
				func(result *model.TotalQuantityResultForTrend) *model.TotalQuantityResultForTrend { return result }).
			ToSlice(&secondGroupResultsForQuantity)
		quantityResultsMap := make(map[int]map[int]int)
		for _, secondGroupElements := range secondGroupResultsForQuantity {
			stageId := secondGroupElements.Key.(int)
			resultsMap := make(map[int]int)
			linq.From(secondGroupElements.Group).
				ToMapByT(&resultsMap,
					func(el any) int { return el.(*model.TotalQuantityResultForTrend).ItemID },
					func(el any) int { return el.(*model.TotalQuantityResultForTrend).TotalQuantity })
			quantityResultsMap[stageId] = resultsMap
		}
		groupResultsMap[groupId] = quantityResultsMap
	}

	var firstGroupResultsForTimes []linq.Group
	linq.From(timesResults).
		GroupByT(
			func(result *model.TotalTimesResultForTrend) int { return result.GroupID },
			func(result *model.TotalTimesResultForTrend) *model.TotalTimesResultForTrend { return result }).
		ToSlice(&firstGroupResultsForTimes)
	for _, firstGroupElements := range firstGroupResultsForTimes {
		groupId := firstGroupElements.Key.(int)
		quantityResultsMapForOneGroup := groupResultsMap[groupId]
		var secondGroupResultsForTimes []linq.Group
		linq.From(firstGroupElements.Group).
			GroupByT(
				func(result *model.TotalTimesResultForTrend) int { return result.StageID },
				func(result *model.TotalTimesResultForTrend) *model.TotalTimesResultForTrend { return result }).
			ToSlice(&secondGroupResultsForTimes)
		for _, secondGroupElements := range secondGroupResultsForTimes {
			stageId := secondGroupElements.Key.(int)

			if quantityResultsMapForOneGroup == nil {
				// it means no items were dropped in this group, we need to find all dropable items and set their quantity to 0
				dropInfosForSpecialTimeRange, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, []*model.TimeRange{
					{
						StartTime: secondGroupElements.Group[0].(*model.TotalTimesResultForTrend).IntervalStart,
						EndTime:   secondGroupElements.Group[0].(*model.TotalTimesResultForTrend).IntervalEnd,
					},
				}, []int{stageId}, itemIdFilter)
				if err != nil {
					return nil, err
				}
				var dropItemIds []int
				linq.From(dropInfosForSpecialTimeRange).
					WhereT(func(el *model.DropInfo) bool { return el.ItemID.Valid }).
					SelectT(func(el *model.DropInfo) int { return int(el.ItemID.Int64) }).
					ToSlice(&dropItemIds)
				quantityResultsMapForOneGroup = make(map[int]map[int]int)
				subMap := make(map[int]int)
				for _, dropItemId := range dropItemIds {
					subMap[dropItemId] = 0
				}
				quantityResultsMapForOneGroup[stageId] = subMap
			}

			resultsMap := quantityResultsMapForOneGroup[stageId]
			for _, el := range secondGroupElements.Group {
				times := el.(*model.TotalTimesResultForTrend).TotalTimes
				startTime := el.(*model.TotalTimesResultForTrend).IntervalStart
				endTime := el.(*model.TotalTimesResultForTrend).IntervalEnd
				for itemId, quantity := range resultsMap {
					combinedResults = append(combinedResults, &model.CombinedResultForTrend{
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
	return combinedResults, nil
}

func (s *Trend) convertTrendElementsToTrendQueryResult(trendElements []*model.TrendElement) (*model.TrendQueryResult, error) {
	var groupedResults []linq.Group
	linq.From(trendElements).
		GroupByT(
			func(el *model.TrendElement) int { return el.StageID },
			func(el *model.TrendElement) *model.TrendElement { return el },
		).
		ToSlice(&groupedResults)
	trendQueryResult := &model.TrendQueryResult{
		Trends: make([]*model.StageTrend, 0),
	}
	for _, el := range groupedResults {
		stageId := el.Key.(int)
		stageTrend := &model.StageTrend{
			StageID: stageId,
			Results: make([]*model.ItemTrend, 0),
		}
		var groupedResults2 []linq.Group
		linq.From(el.Group).
			GroupByT(
				func(el *model.TrendElement) int { return el.ItemID },
				func(el *model.TrendElement) *model.TrendElement { return el },
			).
			ToSlice(&groupedResults2)
		var startTime *time.Time
		for _, el2 := range groupedResults2 {
			itemId := el2.Key.(int)
			var sortedElements []*model.TrendElement
			linq.From(el2.Group).
				SortT(func(el1, el2 *model.TrendElement) bool { return el1.GroupID < el2.GroupID }).
				ToSlice(&sortedElements)
			startTime = sortedElements[0].StartTime
			minGroupId := linq.From(sortedElements).SelectT(func(el *model.TrendElement) int { return el.GroupID }).Min().(int)
			maxGroupId := linq.From(sortedElements).SelectT(func(el *model.TrendElement) int { return el.GroupID }).Max().(int)
			timesArray := make([]int, maxGroupId+1)
			quantityArray := make([]int, maxGroupId+1)
			for _, el3 := range sortedElements {
				timesArray[el3.GroupID] = el3.Times
				quantityArray[el3.GroupID] = el3.Quantity
			}
			stageTrend.Results = append(stageTrend.Results, &model.ItemTrend{
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

func (s *Trend) applyShimForCustomizedTrendQuery(ctx context.Context, queryResult *model.TrendQueryResult, startTime *time.Time) (*modelv2.TrendQueryResult, error) {
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

func (s *Trend) applyShimForSavedTrendQuery(ctx context.Context, server string, queryResult *model.TrendQueryResult) (*modelv2.TrendQueryResult, error) {
	shimMinStartTime := gameday.EndTime(server, time.Now()).Add(-1 * constant.DefaultIntervalNum * 24 * time.Hour)
	currentGameDayEndTime := gameday.EndTime(server, time.Now())

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
