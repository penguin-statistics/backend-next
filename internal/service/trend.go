package service

import (
	"context"
	"sort"
	"time"

	"exusiai.dev/gommon/constant"
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
		return s.calcShimTrend(ctx, server)
	}

	var shimResult modelv2.TrendQueryResult
	key := server
	publish := cache.PublishingLastModifiedTime[modelv2.TrendQueryResult]("[shimTrend#server:" + key + "]")
	if _, err := cache.ShimTrend.MutexGetSetPublishing(key, &shimResult, valueFunc, 24*time.Hour, publish); err != nil {
		return nil, err
	}
	return &shimResult, nil
}

// WarmUpShimTrend fills the cached trend of server as a request does: it skips it if it is cached, and waits for a
// calculation a request runs instead of running another one. RefreshShimTrend recalculates it instead.
func (s *Trend) WarmUpShimTrend(ctx context.Context, server string) error {
	_, err := s.GetShimTrend(ctx, server)
	return err
}

// RefreshShimTrend recalculates the trend of server from the drop matrix elements and replaces the cached one.
// Requests keep getting the previous one in the meantime instead of waiting for a recalculation, unless the cache is
// cold: then they wait for the refresh, see cache.Set.Refresh. The trend is not cached if it was dropped or replaced
// meanwhile (e.g. purged after an admin recalculation of the elements): it may have been calculated from the elements
// before. If the recalculation fails, the cached one is dropped, also if it panics (the worker recovers the panic).
// The trend is cached while its last modified time is recorded, as a request's calculation is (see
// cache.SetLastModifiedTime).
func (s *Trend) RefreshShimTrend(ctx context.Context, server string) error {
	refreshed := false
	defer func() {
		if !refreshed {
			cache.ShimTrend.Delete(server)
		}
	}()
	valueFunc := func() (*modelv2.TrendQueryResult, error) {
		return s.calcShimTrend(ctx, server)
	}
	publish := cache.PublishingLastModifiedTime[modelv2.TrendQueryResult]("[shimTrend#server:" + server + "]")
	if _, err := cache.ShimTrend.Refresh(server, valueFunc, 24*time.Hour, publish); err != nil {
		return err
	}
	refreshed = true
	return nil
}

func (s *Trend) calcShimTrend(ctx context.Context, server string) (*modelv2.TrendQueryResult, error) {
	queryResult, err := s.calcTrendFromDropMatrixElements(ctx, server)
	if err != nil {
		return nil, err
	}
	return s.applyShimForTrendQuery(ctx, queryResult, nil)
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
	combinedResults := make([]*model.CombinedResultForTrend, 0)
	// group id -> stage id -> item id -> quantity; of results with the same ids the last one is kept
	groupResultsMap := make(map[int]map[int]map[int]int)
	for _, result := range quantityResults {
		quantityResultsMap, ok := groupResultsMap[result.GroupID]
		if !ok {
			quantityResultsMap = make(map[int]map[int]int)
			groupResultsMap[result.GroupID] = quantityResultsMap
		}
		resultsMap, ok := quantityResultsMap[result.StageID]
		if !ok {
			resultsMap = make(map[int]int)
			quantityResultsMap[result.StageID] = resultsMap
		}
		resultsMap[result.ItemID] = result.TotalQuantity
	}

	// times results grouped by group id, then by stage id, in the order the ids first appear
	for _, firstGroupElements := range util.GroupBy(timesResults, func(result *model.TotalTimesResultForTrend) int { return result.GroupID }) {
		groupId := firstGroupElements.Key
		quantityResultsMapForOneGroup := groupResultsMap[groupId]
		for _, secondGroupElements := range util.GroupBy(firstGroupElements.Items, func(result *model.TotalTimesResultForTrend) int { return result.StageID }) {
			stageId := secondGroupElements.Key

			if quantityResultsMapForOneGroup == nil {
				// it means no items were dropped in this group, we need to find all dropable items and set their quantity to 0
				dropInfosForSpecialTimeRange, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, []*model.TimeRange{
					{
						StartTime: secondGroupElements.Items[0].IntervalStart,
						EndTime:   secondGroupElements.Items[0].IntervalEnd,
					},
				}, []int{stageId}, itemIdFilter)
				if err != nil {
					return nil, err
				}
				quantityResultsMapForOneGroup = make(map[int]map[int]int)
				subMap := make(map[int]int)
				for _, dropInfo := range dropInfosForSpecialTimeRange {
					if dropInfo.ItemID.Valid {
						subMap[int(dropInfo.ItemID.Int64)] = 0
					}
				}
				quantityResultsMapForOneGroup[stageId] = subMap
			}

			resultsMap := quantityResultsMapForOneGroup[stageId]
			for _, el := range secondGroupElements.Items {
				times := el.TotalTimes
				startTime := el.IntervalStart
				endTime := el.IntervalEnd
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
	trendQueryResult := &model.TrendQueryResult{
		Trends: make([]*model.StageTrend, 0),
	}
	// grouped by stage id, then by item id, in the order the ids first appear
	for _, el := range util.GroupBy(trendElements, func(el *model.TrendElement) int { return el.StageID }) {
		stageId := el.Key
		stageTrend := &model.StageTrend{
			StageID: stageId,
			Results: make([]*model.ItemTrend, 0),
		}
		var startTime *time.Time
		for _, el2 := range util.GroupBy(el.Items, func(el *model.TrendElement) int { return el.ItemID }) {
			itemId := el2.Key
			// the items of a group are a new slice, sorted in place
			sortedElements := el2.Items
			sort.Sort(trendElementsByGroupID(sortedElements))
			startTime = sortedElements[0].StartTime
			minGroupId := sortedElements[0].GroupID
			maxGroupId := sortedElements[len(sortedElements)-1].GroupID
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

// trendElementsByGroupID sorts trend elements by group id.
type trendElementsByGroupID []*model.TrendElement

func (s trendElementsByGroupID) Len() int           { return len(s) }
func (s trendElementsByGroupID) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s trendElementsByGroupID) Less(i, j int) bool { return s[i].GroupID < s[j].GroupID }
