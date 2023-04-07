package service

import (
	"context"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/ahmetb/go-linq/v3"
	"github.com/rs/zerolog/log"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/util"
)

type Trend struct {
	DropReportService        *DropReport
	DropInfoService          *DropInfo
	StageService             *Stage
	ItemService              *Item
	DropMatrixElementService *DropMatrixElement
}

func NewTrend(
	dropReportService *DropReport,
	dropInfoService *DropInfo,
	stageService *Stage,
	itemService *Item,
	dropMatrixElementService *DropMatrixElement,
) *Trend {
	return &Trend{
		DropReportService:        dropReportService,
		DropInfoService:          dropInfoService,
		StageService:             stageService,
		ItemService:              itemService,
		DropMatrixElementService: dropMatrixElementService,
	}
}

// =========== Global ===========

// Cache: shimTrend#server:{server}, 24hrs, records last modified time
// Called by frontend, only for global
func (s *Trend) GetShimTrend(ctx context.Context, server string) (*modelv2.TrendQueryResult, error) {
	valueFunc := func() (*modelv2.TrendQueryResult, error) {
		queryResult, err := s.calcTrendFromDropMatrixElements(ctx, server)
		if err != nil {
			return nil, err
		}
		slowShimResult, err := s.applyShimForTrendQuery(ctx, queryResult, nil)
		if err != nil {
			return nil, err
		}
		return slowShimResult, nil
	}

	var shimResult modelv2.TrendQueryResult
	key := server
	calculated, err := cache.ShimTrend.MutexGetSet(key, &shimResult, valueFunc, 24*time.Hour)
	if err != nil {
		return nil, err
	} else if calculated {
		cache.LastModifiedTime.Set("[shimTrend#server:"+key+"]", time.Now(), 0)
	}
	return &shimResult, nil
}

func (s *Trend) calcTrendFromDropMatrixElements(ctx context.Context, server string) (*model.TrendQueryResult, error) {
	trendQueryResult := &model.TrendQueryResult{
		Trends: make([]*model.StageTrend, 0),
	}
	today := time.Now()
	endDayNum := util.GetDayNum(&today, server)
	startDayNum := endDayNum - constant.DefaultIntervalNum + 1
	dropMatrixElements, err := s.DropMatrixElementService.GetElementsByServerAndSourceCategoryAndDayNumRange(ctx, server, constant.SourceCategoryAll, startDayNum, endDayNum)
	if err != nil {
		return nil, err
	}
	elementsMap := util.GetDropMatrixElementsMap(dropMatrixElements, false)
	for stageId, elementsMapByItemId := range elementsMap {
		stageTrend := &model.StageTrend{
			StageID: stageId,
			Results: make([]*model.ItemTrend, 0),
		}
		for itemId, elementsByDayNum := range elementsMapByItemId {
			times := make([]int, constant.DefaultIntervalNum)
			quantity := make([]int, constant.DefaultIntervalNum)
			minDayNum := endDayNum
			for dayNum, element := range elementsByDayNum {
				if dayNum < minDayNum {
					minDayNum = dayNum
				}
				times[dayNum-startDayNum] = element.Times
				quantity[dayNum-startDayNum] = element.Quantity
			}
			// remove heading zeros, totally (minDayNum - startDayNum) zeros
			times = times[minDayNum-startDayNum:]
			quantity = quantity[minDayNum-startDayNum:]

			startTime := time.UnixMilli(util.GetDayStartTimestampFromDayNum(minDayNum, server))
			itemTrend := &model.ItemTrend{
				ItemID:    itemId,
				StartTime: &startTime,
				Times:     times,
				Quantity:  quantity,
			}
			stageTrend.Results = append(stageTrend.Results, itemTrend)
		}
		trendQueryResult.Trends = append(trendQueryResult.Trends, stageTrend)
	}
	return trendQueryResult, nil
}

// =========== Customized ===========

func (s *Trend) GetShimCustomizedTrendResults(
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIds []int, itemIds []int, accountId null.Int, sourceCategory string,
) (*modelv2.TrendQueryResult, error) {
	trendQueryResult, err := s.queryTrend(ctx, server, startTime, intervalLength, intervalNum, stageIds, itemIds, accountId, sourceCategory)
	if err != nil {
		return nil, err
	}
	return s.applyShimForTrendQuery(ctx, trendQueryResult, startTime)
}

func (s *Trend) queryTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIdFilter []int, itemIdFilter []int, accountId null.Int, sourceCategory string,
) (*model.TrendQueryResult, error) {
	trendElements, err := s.calcTrend(ctx, server, startTime, intervalLength, intervalNum, stageIdFilter, itemIdFilter, accountId, sourceCategory)
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

// =========== Helpers ===========

func (s *Trend) applyShimForTrendQuery(ctx context.Context, queryResult *model.TrendQueryResult, startTime *time.Time) (*modelv2.TrendQueryResult, error) {
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
		if startTime != nil {
			shimStageTrend.StartTime = startTime.UnixMilli()
		}

		var minStartTime *time.Time
		for _, itemTrend := range stageTrend.Results {
			item := itemsMapById[itemTrend.ItemID]
			shimStageTrend.Results[item.ArkItemID] = &modelv2.OneItemTrend{
				Quantity: itemTrend.Quantity,
				Times:    itemTrend.Times,
			}
			if minStartTime == nil || itemTrend.StartTime.Before(*minStartTime) {
				minStartTime = itemTrend.StartTime
			}
		}
		if startTime == nil {
			shimStageTrend.StartTime = minStartTime.UnixMilli()
		}
		if len(shimStageTrend.Results) > 0 {
			results.Trend[stage.ArkStageID] = &shimStageTrend
		}
	}
	return results, nil
}
