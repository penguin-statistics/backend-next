package service

import (
	"context"
	"strconv"
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/util"
)

type DropMatrix struct {
	Config                   *appconfig.Config
	TimeRangeService         *TimeRange
	DropReportService        *DropReport
	DropInfoService          *DropInfo
	DropMatrixElementService *DropMatrixElement
	StageService             *Stage
	ItemService              *Item
	SiteStatsService         *SiteStats
}

func NewDropMatrix(
	config *appconfig.Config,
	timeRangeService *TimeRange,
	dropReportService *DropReport,
	dropInfoService *DropInfo,
	dropMatrixElementService *DropMatrixElement,
	stageService *Stage,
	itemService *Item,
	siteStatsService *SiteStats,
) *DropMatrix {
	return &DropMatrix{
		Config:                   config,
		TimeRangeService:         timeRangeService,
		DropReportService:        dropReportService,
		DropInfoService:          dropInfoService,
		DropMatrixElementService: dropMatrixElementService,
		StageService:             stageService,
		ItemService:              itemService,
		SiteStatsService:         siteStatsService,
	}
}

// =========== Global & Personal, Max Accumulable ===========

// Cache: shimGlobalDropMatrix#server|showClosedZones|sourceCategory:{server}|{showClosedZones}|{sourceCategory}, 24 hrs, records last modified time
// Called by frontend, used for both global and personal, only for max accumulable results
func (s *DropMatrix) GetShimDropMatrix(
	ctx context.Context, server string, showClosedZones bool, stageFilterStr string, itemFilterStr string, accountId null.Int, sourceCategory string,
) (*modelv2.DropMatrixQueryResult, error) {
	valueFunc := func() (*modelv2.DropMatrixQueryResult, error) {
		var dropMatrixQueryResult *model.DropMatrixQueryResult
		var err error
		if accountId.Valid {
			dropMatrixQueryResult, err = s.getMaxAccumulableDropMatrixResults(ctx, server, accountId, sourceCategory)
		} else {
			dropMatrixQueryResult, err = s.calcGlobalDropMatrix(ctx, server, sourceCategory)
		}
		if err != nil {
			return nil, err
		}
		var openingStageIds map[int]struct{}
		if !showClosedZones {
			if openingStageIds, err = s.getOpeningStageIds(ctx, server); err != nil {
				return nil, err
			}
		}
		slowResults, err := s.applyShimForDropMatrixQuery(ctx, showClosedZones, openingStageIds, stageFilterStr, itemFilterStr, dropMatrixQueryResult)
		if err != nil {
			return nil, err
		}
		return slowResults, nil
	}

	var results modelv2.DropMatrixQueryResult
	if !accountId.Valid && stageFilterStr == "" && itemFilterStr == "" {
		key := server + constant.CacheSep + strconv.FormatBool(showClosedZones) + constant.CacheSep + sourceCategory
		publish := cache.PublishingLastModifiedTime[modelv2.DropMatrixQueryResult]("[shimGlobalDropMatrix#server|showClosedZones|sourceCategory:" + key + "]")
		if _, err := cache.ShimGlobalDropMatrix.MutexGetSetPublishing(key, &results, valueFunc, 24*time.Hour, publish); err != nil {
			return nil, err
		}
	} else {
		return valueFunc()
	}
	return &results, nil
}

// WarmUpGlobalDropMatrixCaches fills the cached global drop matrices of server (for every source category, raw and
// shim with and without closed zones) as requests do: it skips those cached, and waits for a calculation a request
// runs instead of running another one. RefreshGlobalDropMatrixCaches recalculates them instead.
func (s *DropMatrix) WarmUpGlobalDropMatrixCaches(ctx context.Context, server string) error {
	for _, sourceCategory := range s.Config.MatrixWorkerSourceCategories {
		for _, showClosedZones := range []bool{true, false} {
			if _, err := s.GetShimDropMatrix(ctx, server, showClosedZones, "", "", null.Int{}, sourceCategory); err != nil {
				return err
			}
		}
	}
	return nil
}

// =========== Global Max Accumulable ===========

// Calc today's drop matrix elements and save to DB
// Called by worker
func (s *DropMatrix) RunCalcDropMatrixJob(ctx context.Context, server string) error {
	date := util.Now()
	endTime := time.UnixMilli(constant.FakeEndTimeMilli)
	dropMatrixElements, err := s.calcDropMatrixByGivenDate(ctx, server, &date, &endTime, s.Config.MatrixWorkerSourceCategories)
	if err != nil {
		return err
	}
	dayNum := util.GetDayNum(&date, server)
	exists, err := s.DropMatrixElementService.IsExistByServerAndDayNum(ctx, server, dayNum)
	if err != nil {
		return err
	}
	s.DropMatrixElementService.DeleteByServerAndDayNum(ctx, server, dayNum)

	if len(dropMatrixElements) != 0 {
		s.DropMatrixElementService.BatchSaveElements(ctx, dropMatrixElements, server)
	}
	describeCalcJob(ctx, dayNum, s.Config.MatrixWorkerSourceCategories, len(dropMatrixElements))

	// If this is the first time we run the job for this server at this day, we need to update the drop matrix for the previous day.
	// TODO: archive all drop reports for the today-60d and upload to s3
	if !exists {
		yesterday := date.Add(time.Hour * -24)
		dropMatrixElementsForYesterday, err := s.calcDropMatrixByGivenDate(ctx, server, &yesterday, nil, s.Config.MatrixWorkerSourceCategories)
		if err != nil {
			return err
		}
		s.DropMatrixElementService.DeleteByServerAndDayNum(ctx, server, dayNum-1)
		if len(dropMatrixElementsForYesterday) != 0 {
			s.DropMatrixElementService.BatchSaveElements(ctx, dropMatrixElementsForYesterday, server)
		}
		describePreviousDay(ctx, len(dropMatrixElementsForYesterday))
	}

	// the global drop matrices and the trend are replaced by RefreshGlobalDropMatrixCaches and
	// Trend.RefreshShimTrend, not dropped: requests keep getting the previous ones while new ones are calculated
	return nil
}

// Update drop matrix elements for a given date (entire day)
// Called by admin api
func (s *DropMatrix) UpdateDropMatrixByGivenDate(ctx context.Context, server string, date *time.Time) error {
	dropMatrixElements, err := s.calcDropMatrixByGivenDate(ctx, server, date, nil, s.Config.MatrixWorkerSourceCategories)
	if err != nil {
		return err
	}
	dayNum := util.GetDayNum(date, server)
	s.DropMatrixElementService.DeleteByServerAndDayNum(ctx, server, dayNum)
	if len(dropMatrixElements) != 0 {
		s.DropMatrixElementService.BatchSaveElements(ctx, dropMatrixElements, server)
	}
	// the day may be one of those the site stats keep aggregated in memory
	return s.SiteStatsService.InvalidateSiteStatsBase(ctx, server)
}

/**
 * Calculate drop matrix for a given date
 * date: indicates the date to calculate drop matrix
 * endTime: if nil, the calculation will be done for the entire day; otherwise, the calculation will be done for the partial day
 */
func (s *DropMatrix) calcDropMatrixByGivenDate(
	ctx context.Context, server string, date *time.Time, endTime *time.Time, sourceCategories []string,
) ([]*model.DropMatrixElement, error) {
	dropMatrixElements := make([]*model.DropMatrixElement, 0)

	start := time.UnixMilli(util.GetDayStartTime(date, server))
	startNextDay := start.Add(time.Hour * 24)
	end := lo.Ternary(endTime == nil, &startNextDay, endTime)

	timeRangeGiven := &model.TimeRange{
		StartTime: &start,
		EndTime:   end,
	}

	timeRangesMap, err := s.TimeRangeService.GetAllMaxAccumulableTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	stageIdsItemIdsMapByTimeRangeStr := make(map[string]map[int][]int, 0)
	for stageId, timeRangesMapByItemId := range timeRangesMap {
		for itemId, timeRanges := range timeRangesMapByItemId {
			for _, timeRange := range timeRanges {
				intersection := util.GetIntersection(timeRange, timeRangeGiven)
				if intersection == nil {
					continue
				}
				intersectionStr := intersection.String()
				if _, ok := stageIdsItemIdsMapByTimeRangeStr[intersectionStr]; !ok {
					stageIdsItemIdsMapByTimeRangeStr[intersectionStr] = make(map[int][]int, 0)
				}
				if _, ok := stageIdsItemIdsMapByTimeRangeStr[intersectionStr][stageId]; !ok {
					stageIdsItemIdsMapByTimeRangeStr[intersectionStr][stageId] = make([]int, 0)
				}
				stageIdsItemIdsMapByTimeRangeStr[intersectionStr][stageId] = append(stageIdsItemIdsMapByTimeRangeStr[intersectionStr][stageId], itemId)
			}
		}
	}

	queryCtxs := make([]*model.DropReportQueryContext, 0, len(stageIdsItemIdsMapByTimeRangeStr))
	for timeRangeStr, stageIdsItemIdsMap := range stageIdsItemIdsMapByTimeRangeStr {
		timeRange := model.TimeRangeFromString(timeRangeStr)
		stageIdsItemIdsMap := stageIdsItemIdsMap
		queryCtxs = append(queryCtxs, &model.DropReportQueryContext{
			Server:             server,
			StartTime:          timeRange.StartTime,
			EndTime:            timeRange.EndTime,
			ExcludeNonOneTimes: false,
			StageItemFilter:    &stageIdsItemIdsMap,
		})
	}
	// scan drop_reports once for every time range and source category
	timesResults, quantityUniqCountResults, err := s.DropReportService.CalcDropMatrixForIntervalsBySourceCategories(ctx, queryCtxs, sourceCategories)
	if err != nil {
		return nil, err
	}
	for i, queryCtx := range queryCtxs {
		dropItemIdsByStageId := make(map[int][]int)
		for _, sourceCategory := range sourceCategories {
			categoryQueryCtx := *queryCtx
			categoryQueryCtx.SourceCategory = sourceCategory
			res, err := s.calcDropMatrix(ctx, &categoryQueryCtx,
				timesResults[i][sourceCategory], quantityUniqCountResults[i][sourceCategory], dropItemIdsByStageId)
			if err != nil {
				return nil, err
			}
			dropMatrixElements = append(dropMatrixElements, res...)
		}
	}
	return dropMatrixElements, nil
}

// dropItemIdsByStageId caches the dropped item ids of each stage for queryCtx's time range across calls.
func (s *DropMatrix) calcDropMatrix(
	ctx context.Context, queryCtx *model.DropReportQueryContext, timesResults []*model.TotalTimesResult,
	quantityUniqCountResults []*model.QuantityUniqCountResultForDropMatrix, dropItemIdsByStageId map[int][]int,
) ([]*model.DropMatrixElement, error) {
	var combinedResults []*model.CombinedResultForDropMatrix
	quantityResults := calcTotalQuantityForDropMatrix(quantityUniqCountResults)

	oneBatch := s.combineQuantityAndTimesResults(quantityResults, timesResults, quantityUniqCountResults, nil)
	combinedResults = append(combinedResults, oneBatch...)

	// save stage times for later use
	stageTimesMap := map[int]int{}

	// grouping results by stage id
	stageIds := make([]int, 0)
	combinedResultsByStageId := make(map[int][]*model.CombinedResultForDropMatrix)
	for _, result := range combinedResults {
		if _, ok := combinedResultsByStageId[result.StageID]; !ok {
			stageIds = append(stageIds, result.StageID)
		}
		combinedResultsByStageId[result.StageID] = append(combinedResultsByStageId[result.StageID], result)
	}

	dayNum := util.GetDayNum(queryCtx.StartTime, queryCtx.Server)
	dropMatrixElements := make([]*model.DropMatrixElement, 0)
	for _, stageId := range stageIds {
		itemIds := (*queryCtx.StageItemFilter)[stageId]

		// get all item ids which are dropped in this stage, save in dropSet
		dropItemIds, ok := dropItemIdsByStageId[stageId]
		if !ok {
			timeRange := &model.TimeRange{
				StartTime: queryCtx.StartTime,
				EndTime:   queryCtx.EndTime,
			}
			dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(
				ctx, queryCtx.Server, []*model.TimeRange{timeRange}, []int{stageId}, itemIds)
			if err != nil {
				return nil, err
			}
			itemIdsSet := make(map[int]struct{}, len(itemIds))
			for _, itemId := range itemIds {
				itemIdsSet[itemId] = struct{}{}
			}
			dropItemIds = make([]int, 0, len(dropInfos))
			for _, dropInfo := range dropInfos {
				if !dropInfo.ItemID.Valid {
					continue
				}
				if _, ok := itemIdsSet[int(dropInfo.ItemID.Int64)]; ok {
					dropItemIds = append(dropItemIds, int(dropInfo.ItemID.Int64))
				}
			}
			dropItemIdsByStageId[stageId] = dropItemIds
		}
		// use a fake hashset to save item ids
		dropSet := make(map[int]struct{}, len(dropItemIds))
		for _, itemId := range dropItemIds {
			dropSet[itemId] = struct{}{}
		}

		for _, result := range combinedResultsByStageId[stageId] {
			itemId := result.ItemID
			times := result.Times
			dropMatrixElement := model.DropMatrixElement{
				StageID:         stageId,
				ItemID:          itemId,
				Quantity:        result.Quantity,
				QuantityBuckets: result.QuantityBuckets,
				Times:           times,
				Server:          queryCtx.Server,
				SourceCategory:  queryCtx.SourceCategory,
				StartTime:       queryCtx.StartTime,
				EndTime:         queryCtx.EndTime,
				DayNum:          dayNum,
			}
			dropMatrixElements = append(dropMatrixElements, &dropMatrixElement)
			delete(dropSet, itemId)        // remove existing item ids from drop set
			stageTimesMap[stageId] = times // record stage times into a map
		}
		// add those items which do not show up in the matrix (quantity is 0)
		for itemId := range dropSet {
			times := stageTimesMap[stageId]
			dropMatrixElementWithZeroQuantity := model.DropMatrixElement{
				StageID:         stageId,
				ItemID:          itemId,
				Quantity:        0,
				QuantityBuckets: map[int]int{0: times},
				Times:           times,
				Server:          queryCtx.Server,
				SourceCategory:  queryCtx.SourceCategory,
				StartTime:       queryCtx.StartTime,
				EndTime:         queryCtx.EndTime,
				DayNum:          dayNum,
			}
			dropMatrixElements = append(dropMatrixElements, &dropMatrixElementWithZeroQuantity)
		}
	}
	return dropMatrixElements, nil
}

// Cache: globalDropMatrix#server|sourceCategory:{server}|{sourceCategory}, 24 hrs
func (s *DropMatrix) calcGlobalDropMatrix(ctx context.Context, server string, sourceCategory string) (*model.DropMatrixQueryResult, error) {
	valueFunc := func() (*model.DropMatrixQueryResult, error) {
		return s.calcGlobalDropMatrixUncached(ctx, server, sourceCategory)
	}

	var results model.DropMatrixQueryResult
	key := server + constant.CacheSep + sourceCategory
	_, err := cache.GlobalDropMatrix.MutexGetSet(key, &results, valueFunc, 24*time.Hour)
	if err != nil {
		return nil, err
	}
	return &results, nil
}

// RefreshGlobalDropMatrixCaches recalculates the global drop matrices of server and replaces the cached ones (for
// every source category, raw and shim with and without closed zones). Requests keep getting the previous ones in
// the meantime instead of all waiting for a recalculation. If the recalculation fails, the cached ones are dropped,
// also if it panics (the worker recovers the panic): the source categories cached before it must not keep matrices
// of this refresh while the others keep those of an earlier one.
func (s *DropMatrix) RefreshGlobalDropMatrixCaches(ctx context.Context, server string) error {
	refreshed := false
	defer func() {
		if !refreshed {
			s.deleteGlobalDropMatrixCaches(server)
		}
	}()
	globals, err := s.calcGlobalDropMatrices(ctx, server, s.Config.MatrixWorkerSourceCategories)
	if err != nil {
		return err
	}
	// the opening stages are the same for every source category: read them once
	openingStageIds, err := s.getOpeningStageIds(ctx, server)
	if err != nil {
		return err
	}
	for _, sourceCategory := range s.Config.MatrixWorkerSourceCategories {
		global := globals[sourceCategory]
		shims := make(map[bool]*modelv2.DropMatrixQueryResult, 2)
		for _, showClosedZones := range []bool{true, false} {
			if shims[showClosedZones], err = s.applyShimForDropMatrixQuery(ctx, showClosedZones, openingStageIds, "", "", global); err != nil {
				return err
			}
		}
		cache.GlobalDropMatrix.Set(server+constant.CacheSep+sourceCategory, *global, 24*time.Hour)
		for showClosedZones, shim := range shims {
			key := server + constant.CacheSep + strconv.FormatBool(showClosedZones) + constant.CacheSep + sourceCategory
			cache.SetLastModifiedTime("[shimGlobalDropMatrix#server|showClosedZones|sourceCategory:"+key+"]", *shim, func() {
				cache.ShimGlobalDropMatrix.Set(key, *shim, 24*time.Hour)
			})
		}
	}
	refreshed = true
	return nil
}

func (s *DropMatrix) deleteGlobalDropMatrixCaches(server string) {
	for _, sourceCategory := range s.Config.MatrixWorkerSourceCategories {
		cache.GlobalDropMatrix.Delete(server + constant.CacheSep + sourceCategory)
		for _, showClosedZones := range []string{"true", "false"} {
			cache.ShimGlobalDropMatrix.Delete(server + constant.CacheSep + showClosedZones + constant.CacheSep + sourceCategory)
		}
	}
}

func (s *DropMatrix) calcGlobalDropMatrixUncached(ctx context.Context, server string, sourceCategory string) (*model.DropMatrixQueryResult, error) {
	results, err := s.calcGlobalDropMatrices(ctx, server, []string{sourceCategory})
	if err != nil {
		return nil, err
	}
	return results[sourceCategory], nil
}

// calcGlobalDropMatrices calculates the global drop matrices of server for every source category: each stage and
// item over its latest max accumulable time range, from a single statement per source category.
func (s *DropMatrix) calcGlobalDropMatrices(ctx context.Context, server string, sourceCategories []string) (map[string]*model.DropMatrixQueryResult, error) {
	maxAccumulableTimeRanges, err := s.TimeRangeService.GetAllMaxAccumulableTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}

	// Only consider the latest (max accumulable) time range for each stage
	targets := make([]*model.GlobalDropMatrixTarget, 0)
	for stageId, timeRangesMapByItemId := range maxAccumulableTimeRanges {
		for itemId, timeRanges := range timeRangesMapByItemId {
			// as the elements had it: start and end only, at second precision
			timeRange := model.TimeRangeFromString(timeRanges[len(timeRanges)-1].String())
			targets = append(targets, &model.GlobalDropMatrixTarget{StageID: stageId, ItemID: itemId, TimeRange: timeRange})
		}
	}
	// a statement per source category: those of all source categories at once aggregate three times the elements
	// and spill to disk, which is slower than three statements
	aggregates := make(map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate)
	for _, sourceCategory := range sourceCategories {
		categoryAggregates, err := s.DropMatrixElementService.CalcGlobalDropMatrixAggregates(ctx, server, targets, []string{sourceCategory})
		if err != nil {
			return nil, err
		}
		for key, aggregate := range categoryAggregates {
			aggregates[key] = aggregate
		}
	}

	results := make(map[string]*model.DropMatrixQueryResult, len(sourceCategories))
	for _, sourceCategory := range sourceCategories {
		result := &model.DropMatrixQueryResult{
			Matrix: make([]*model.OneDropMatrixElement, 0),
		}
		for _, target := range targets {
			aggregate, ok := aggregates[model.GlobalDropMatrixKey{SourceCategory: sourceCategory, StageID: target.StageID, ItemID: target.ItemID}]
			// an element needs times, quantity and quantity buckets
			if !ok || !aggregate.Times.Valid || !aggregate.Quantity.Valid || len(aggregate.QuantityBuckets) == 0 {
				continue
			}
			times := int(aggregate.Times.Int64)
			result.Matrix = append(result.Matrix, &model.OneDropMatrixElement{
				StageID:   target.StageID,
				ItemID:    target.ItemID,
				Times:     times,
				Quantity:  int(aggregate.Quantity.Int64),
				TimeRange: target.TimeRange,
				StdDev:    util.RoundFloat64(util.CalcStdDevFromQuantityBuckets(aggregate.QuantityBuckets, times, false), constant.StdDevDigits),
			})
		}
		results[sourceCategory] = result
	}
	return results, nil
}

// =========== Personal Max Accumulable ===========

func (s *DropMatrix) getMaxAccumulableDropMatrixResults(ctx context.Context, server string, accountId null.Int, sourceCategory string) (*model.DropMatrixQueryResult, error) {
	dropMatrixElements, err := s.getDropMatrixElements(ctx, server, accountId, sourceCategory)
	if err != nil {
		return nil, err
	}
	return s.convertDropMatrixElementsToMaxAccumulableDropMatrixQueryResult(ctx, server, dropMatrixElements)
}

func (s *DropMatrix) getDropMatrixElements(ctx context.Context, server string, accountId null.Int, sourceCategory string) ([]*model.DropMatrixElement, error) {
	maxAccumulableTimeRanges, err := s.TimeRangeService.GetMaxAccumulableTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	timeRanges := make([]*model.TimeRange, 0)

	timeRangesMap := make(map[int]*model.TimeRange)
	for _, maxAccumulableTimeRangesForOneStage := range maxAccumulableTimeRanges {
		for _, timeRanges := range maxAccumulableTimeRangesForOneStage {
			for _, timeRange := range timeRanges {
				timeRangesMap[timeRange.RangeID] = timeRange
			}
		}
	}
	for _, timeRange := range timeRangesMap {
		timeRanges = append(timeRanges, timeRange)
	}
	dropMatrixElements, err := s.calcDropMatrixForTimeRanges(ctx, server, timeRanges, nil, nil, accountId, sourceCategory)
	if err != nil {
		return nil, err
	}
	return dropMatrixElements, nil
}

func (s *DropMatrix) convertDropMatrixElementsToMaxAccumulableDropMatrixQueryResult(
	ctx context.Context, server string, dropMatrixElements []*model.DropMatrixElement,
) (*model.DropMatrixQueryResult, error) {
	elementsMap := util.GetDropMatrixElementsMap(dropMatrixElements, true)
	result := &model.DropMatrixQueryResult{
		Matrix: make([]*model.OneDropMatrixElement, 0),
	}
	maxAccumulableTimeRanges, err := s.TimeRangeService.GetMaxAccumulableTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	for stageId, maxAccumulableTimeRangesForOneStage := range maxAccumulableTimeRanges {
		subMapByItemId := elementsMap[stageId]
		for itemId, timeRanges := range maxAccumulableTimeRangesForOneStage {
			subMapByRangeId := subMapByItemId[itemId]
			startTime := timeRanges[0].StartTime
			endTime := timeRanges[0].EndTime
			var combinedDropMatrixResult *model.OneDropMatrixElement
			combinedDropMatrixResult = nil
			for _, timeRange := range timeRanges {
				element, ok := subMapByRangeId[timeRange.RangeID]
				if !ok {
					continue
				}
				oneElementResult := &model.OneDropMatrixElement{
					StageID:  stageId,
					ItemID:   itemId,
					Quantity: element.Quantity,
					Times:    element.Times,
					StdDev:   util.RoundFloat64(util.CalcStdDevFromQuantityBuckets(element.QuantityBuckets, element.Times, false), constant.StdDevDigits),
				}
				if timeRange.StartTime.Before(*startTime) {
					startTime = timeRange.StartTime
				}
				if timeRange.EndTime.After(*endTime) {
					endTime = timeRange.EndTime
				}
				if combinedDropMatrixResult == nil {
					combinedDropMatrixResult = oneElementResult
				} else {
					combinedDropMatrixResult, err = s.combineDropMatrixResults(combinedDropMatrixResult, oneElementResult)
					if err != nil {
						return nil, err
					}
				}
			}
			if combinedDropMatrixResult != nil {
				combinedDropMatrixResult.TimeRange = &model.TimeRange{
					StartTime: startTime,
					EndTime:   endTime,
				}
				result.Matrix = append(result.Matrix, combinedDropMatrixResult)
			}
		}
	}
	return result, nil
}

// =========== Customized ===========

func (s *DropMatrix) GetShimCustomizedDropMatrixResults(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIds []int, itemIds []int, accountId null.Int, sourceCategory string,
) (*modelv2.DropMatrixQueryResult, error) {
	timeRanges := []*model.TimeRange{timeRange}
	dropMatrixElements, err := s.calcDropMatrixForTimeRanges(ctx, server, timeRanges, stageIds, itemIds, accountId, sourceCategory)
	if err != nil {
		return nil, err
	}
	customizedDropMatrixQueryResult, err := s.convertDropMatrixElementsToDropMatrixQueryResult(ctx, dropMatrixElements)
	if err != nil {
		return nil, err
	}
	return s.applyShimForDropMatrixQuery(ctx, true, nil, "", "", customizedDropMatrixQueryResult)
}

func (s *DropMatrix) convertDropMatrixElementsToDropMatrixQueryResult(ctx context.Context, dropMatrixElements []*model.DropMatrixElement) (*model.DropMatrixQueryResult, error) {
	dropMatrixQueryResult := &model.DropMatrixQueryResult{
		Matrix: make([]*model.OneDropMatrixElement, 0),
	}
	// grouped by range id, in the order the range ids first appear
	for _, group := range util.GroupBy(dropMatrixElements, func(el *model.DropMatrixElement) int { return el.RangeID }) {
		rangeId := group.Key
		var timeRange *model.TimeRange
		if rangeId == 0 {
			timeRange = group.Items[0].TimeRange
		} else {
			tr, err := s.TimeRangeService.GetTimeRangeById(ctx, rangeId)
			if err != nil {
				return nil, err
			}
			timeRange = tr
		}

		for _, dropMatrixElement := range group.Items {
			dropMatrixQueryResult.Matrix = append(dropMatrixQueryResult.Matrix, &model.OneDropMatrixElement{
				StageID:   dropMatrixElement.StageID,
				ItemID:    dropMatrixElement.ItemID,
				Quantity:  dropMatrixElement.Quantity,
				Times:     dropMatrixElement.Times,
				StdDev:    util.RoundFloat64(util.CalcStdDevFromQuantityBuckets(dropMatrixElement.QuantityBuckets, dropMatrixElement.Times, false), constant.StdDevDigits),
				TimeRange: timeRange,
			})
		}
	}
	return dropMatrixQueryResult, nil
}

// =========== Helpers ===========

// Called in Personal Max Accumulable and Customized
func (s *DropMatrix) calcDropMatrixForTimeRanges(
	ctx context.Context, server string, timeRanges []*model.TimeRange, stageIdFilter []int, itemIdFilter []int, accountId null.Int, sourceCategory string,
) ([]*model.DropMatrixElement, error) {
	dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, timeRanges, stageIdFilter, itemIdFilter)
	if err != nil {
		return nil, err
	}

	stageItemFilter := util.GetStageIdItemIdMapFromDropInfos(dropInfos)
	queryCtxs := make([]*model.DropReportQueryContext, 0, len(timeRanges))
	for _, timeRange := range timeRanges {
		queryCtxs = append(queryCtxs, &model.DropReportQueryContext{
			Server:             server,
			StartTime:          timeRange.StartTime,
			EndTime:            timeRange.EndTime,
			AccountID:          accountId,
			StageItemFilter:    &stageItemFilter,
			ExcludeNonOneTimes: false,
		})
	}
	var combinedResults []*model.CombinedResultForDropMatrix
	// scan drop_reports once for all time ranges instead of twice per time range (a personal matrix has ~190); with no
	// time range (e.g. a server without any yet), there is nothing to scan
	if len(queryCtxs) > 0 {
		timesResults, quantityUniqCountResults, err := s.DropReportService.CalcDropMatrixForIntervalsBySourceCategories(ctx, queryCtxs, []string{sourceCategory})
		if err != nil {
			return nil, err
		}
		for i, timeRange := range timeRanges {
			quantityResults := calcTotalQuantityForDropMatrix(quantityUniqCountResults[i][sourceCategory])
			oneBatch := s.combineQuantityAndTimesResults(quantityResults, timesResults[i][sourceCategory], quantityUniqCountResults[i][sourceCategory], timeRange)
			combinedResults = append(combinedResults, oneBatch...)
		}
	}

	// save stage times for later use
	stageTimesMap := map[int]int{}

	// grouping results by stage id, then by range id, in the order the ids first appear
	dropMatrixElements := make([]*model.DropMatrixElement, 0)
	for _, el := range util.GroupBy(combinedResults, func(el *model.CombinedResultForDropMatrix) int { return el.StageID }) {
		stageId := el.Key
		for _, el2 := range util.GroupBy(el.Items, func(el *model.CombinedResultForDropMatrix) int { return el.TimeRange.RangeID }) {
			rangeId := el2.Key
			timeRange := el2.Items[0].TimeRange

			// get all item ids which are dropped in this stage and in this time range
			var dropItemIds []int
			if rangeId == 0 {
				// rangeId == 0 means it is a customized time range instead of a time range from the database
				dropInfosForSpecialTimeRange, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, []*model.TimeRange{el2.Items[0].TimeRange}, []int{stageId}, itemIdFilter)
				if err != nil {
					return nil, err
				}
				for _, dropInfo := range dropInfosForSpecialTimeRange {
					if dropInfo.ItemID.Valid {
						dropItemIds = append(dropItemIds, int(dropInfo.ItemID.Int64))
					}
				}
			} else {
				dropItemIds, _ = s.DropInfoService.GetItemDropSetByStageIdAndRangeId(ctx, server, stageId, rangeId)
			}

			// if item id filter is applied, then filter the drop item ids
			if len(itemIdFilter) > 0 {
				dropItemIds = filterItemIds(dropItemIds, itemIdFilter)
			}

			// use a fake hashset to save item ids
			dropSet := make(map[int]struct{}, len(dropItemIds))
			for _, itemId := range dropItemIds {
				dropSet[itemId] = struct{}{}
			}

			for _, el3 := range el2.Items {
				itemId := el3.ItemID
				quantity := el3.Quantity
				times := el3.Times
				quantityBuckets := el3.QuantityBuckets
				dropMatrixElement := model.DropMatrixElement{
					StageID:         stageId,
					ItemID:          itemId,
					RangeID:         rangeId,
					Quantity:        quantity,
					QuantityBuckets: quantityBuckets,
					Times:           times,
					Server:          server,
					SourceCategory:  sourceCategory,
				}
				if rangeId == 0 {
					dropMatrixElement.TimeRange = timeRange
				}
				dropMatrixElements = append(dropMatrixElements, &dropMatrixElement)
				delete(dropSet, itemId)        // remove existing item ids from drop set
				stageTimesMap[stageId] = times // record stage times into a map
			}
			// add those items which do not show up in the matrix (quantity is 0)
			for itemId := range dropSet {
				times := stageTimesMap[stageId]
				dropMatrixElementWithZeroQuantity := model.DropMatrixElement{
					StageID:         stageId,
					ItemID:          itemId,
					RangeID:         rangeId,
					Quantity:        0,
					QuantityBuckets: map[int]int{0: times},
					Times:           times,
					Server:          server,
					SourceCategory:  sourceCategory,
				}
				if rangeId == 0 {
					dropMatrixElementWithZeroQuantity.TimeRange = timeRange
				}
				dropMatrixElements = append(dropMatrixElements, &dropMatrixElementWithZeroQuantity)
			}
		}
	}
	return dropMatrixElements, nil
}

func (s *DropMatrix) combineQuantityAndTimesResults(
	quantityResults []*model.TotalQuantityResultForDropMatrix, timesResults []*model.TotalTimesResult,
	quantityUniqCountResults []*model.QuantityUniqCountResultForDropMatrix, timeRange *model.TimeRange,
) []*model.CombinedResultForDropMatrix {
	combinedResults := make([]*model.CombinedResultForDropMatrix, 0)

	quantityResultsMap := make(map[int]map[int]int)
	for _, result := range quantityResults {
		if _, ok := quantityResultsMap[result.StageID]; !ok {
			quantityResultsMap[result.StageID] = make(map[int]int)
		}
		quantityResultsMap[result.StageID][result.ItemID] = result.TotalQuantity
	}

	quantityUniqCountResultsMap := make(map[int]map[int]map[int]int)
	for _, result := range quantityUniqCountResults {
		if _, ok := quantityUniqCountResultsMap[result.StageID]; !ok {
			quantityUniqCountResultsMap[result.StageID] = make(map[int]map[int]int)
		}
		if _, ok := quantityUniqCountResultsMap[result.StageID][result.ItemID]; !ok {
			quantityUniqCountResultsMap[result.StageID][result.ItemID] = make(map[int]int)
		}
		quantityUniqCountResultsMap[result.StageID][result.ItemID][result.Quantity] = result.Count
	}

	for _, timesResult := range timesResults {
		stageId := timesResult.StageID
		times := timesResult.TotalTimes
		quantityUniqCountResultsMapForOneStage := quantityUniqCountResultsMap[stageId]
		for itemId, quantity := range quantityResultsMap[stageId] {
			quantityBuckets := quantityUniqCountResultsMapForOneStage[itemId]
			if !s.validateQuantityBucketsAndTimes(quantityBuckets, times) {
				log.Warn().Msgf("quantity buckets and times are not matched for stage %d, item %d, please check drop pattern", stageId, itemId)
			}
			combinedResultForDropMatrix := &model.CombinedResultForDropMatrix{
				StageID:         stageId,
				ItemID:          itemId,
				Quantity:        quantity,
				QuantityBuckets: quantityBuckets,
				Times:           times,
			}
			if timeRange != nil {
				combinedResultForDropMatrix.TimeRange = timeRange
			}
			combinedResults = append(combinedResults, combinedResultForDropMatrix)
		}
	}
	return combinedResults
}

func calcTotalQuantityForDropMatrix(uniqCountResults []*model.QuantityUniqCountResultForDropMatrix) []*model.TotalQuantityResultForDropMatrix {
	uniqCountResultsMapByStageIdAndItemId := make(map[int]map[int][]*model.QuantityUniqCountResultForDropMatrix)
	for _, result := range uniqCountResults {
		if _, ok := uniqCountResultsMapByStageIdAndItemId[result.StageID]; !ok {
			uniqCountResultsMapByStageIdAndItemId[result.StageID] = make(map[int][]*model.QuantityUniqCountResultForDropMatrix)
		}
		if _, ok := uniqCountResultsMapByStageIdAndItemId[result.StageID][result.ItemID]; !ok {
			uniqCountResultsMapByStageIdAndItemId[result.StageID][result.ItemID] = make([]*model.QuantityUniqCountResultForDropMatrix, 0)
		}
		uniqCountResultsMapByStageIdAndItemId[result.StageID][result.ItemID] = append(uniqCountResultsMapByStageIdAndItemId[result.StageID][result.ItemID], result)
	}
	results := make([]*model.TotalQuantityResultForDropMatrix, 0)
	for _, uniqCountResultsMapByItemId := range uniqCountResultsMapByStageIdAndItemId {
		for _, uniqCountResults := range uniqCountResultsMapByItemId {
			results = append(results, convertUniqCountResultsForOneItemToQuantityResult(uniqCountResults))
		}
	}
	return results
}

func convertUniqCountResultsForOneItemToQuantityResult(uniqCountResults []*model.QuantityUniqCountResultForDropMatrix) *model.TotalQuantityResultForDropMatrix {
	sum := 0
	for _, result := range uniqCountResults {
		sum += result.Quantity * result.Count
	}
	return &model.TotalQuantityResultForDropMatrix{
		StageID:       uniqCountResults[0].StageID,
		ItemID:        uniqCountResults[0].ItemID,
		TotalQuantity: sum,
	}
}

func (s *DropMatrix) validateQuantityBucketsAndTimes(quantityBuckets map[int]int, times int) bool {
	sum := 0
	for _, quantity := range quantityBuckets {
		sum += quantity
	}
	return sum <= times
}

func (s *DropMatrix) combineDropMatrixResults(a, b *model.OneDropMatrixElement) (*model.OneDropMatrixElement, error) {
	if a.StageID != b.StageID {
		return nil, errors.New("stageId not match")
	}
	if a.ItemID != b.ItemID {
		return nil, errors.New("itemId not match")
	}
	bundleA, err := s.convertOneDropMatrixElementToStatsBundle(a)
	if err != nil {
		return nil, err
	}
	bundleB, err := s.convertOneDropMatrixElementToStatsBundle(b)
	if err != nil {
		return nil, err
	}
	result := &model.OneDropMatrixElement{
		StageID:  a.StageID,
		ItemID:   a.ItemID,
		Quantity: a.Quantity + b.Quantity,
		Times:    a.Times + b.Times,
		StdDev: util.RoundFloat64(
			util.CombineTwoBundles(
				bundleA,
				bundleB,
			).StdDev, constant.StdDevDigits),
	}
	return result, nil
}

func (s *DropMatrix) convertOneDropMatrixElementToStatsBundle(el *model.OneDropMatrixElement) (*util.StatsBundle, error) {
	if el.Times == 0 {
		return nil, errors.New("times should not be 0")
	}
	return &util.StatsBundle{
		N:      el.Times,
		Avg:    float64(el.Quantity) / float64(el.Times),
		StdDev: el.StdDev,
	}, nil
}

// getOpeningStageIds returns the ids of the stages of server with a drop info in a current time range, which a
// shim without closed zones keeps the elements of.
func (s *DropMatrix) getOpeningStageIds(ctx context.Context, server string) (map[int]struct{}, error) {
	stageIds, err := s.DropInfoService.GetOpeningStageIdsByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	openingStageIds := make(map[int]struct{}, len(stageIds))
	for _, stageId := range stageIds {
		openingStageIds[stageId] = struct{}{}
	}
	return openingStageIds, nil
}

// applyShimForDropMatrixQuery converts queryResult to its v2 form, keeping only the elements of openingStageIds (from
// getOpeningStageIds) unless showClosedZones is set.
func (s *DropMatrix) applyShimForDropMatrixQuery(ctx context.Context, showClosedZones bool, openingStageIds map[int]struct{}, stageFilterStr, itemFilterStr string, queryResult *model.DropMatrixQueryResult) (*modelv2.DropMatrixQueryResult, error) {
	itemsMapById, err := s.ItemService.GetItemsMapById(ctx)
	if err != nil {
		return nil, err
	}

	stagesMapById, err := s.StageService.GetStagesMapById(ctx)
	if err != nil {
		return nil, err
	}

	// convert comma-splitted stage filter param to a hashset
	stageFilter := make([]string, 0)
	if stageFilterStr != "" {
		stageFilter = strings.Split(stageFilterStr, ",")
	}
	stageFilterSet := make(map[string]struct{}, len(stageFilter))
	for _, stageIdStr := range stageFilter {
		stageFilterSet[stageIdStr] = struct{}{}
	}

	// convert comma-splitted item filter param to a hashset
	itemFilter := make([]string, 0)
	if itemFilterStr != "" {
		itemFilter = strings.Split(itemFilterStr, ",")
	}
	itemFilterSet := make(map[string]struct{}, len(itemFilter))
	for _, itemIdStr := range itemFilter {
		itemFilterSet[itemIdStr] = struct{}{}
	}

	results := &modelv2.DropMatrixQueryResult{
		Matrix: make([]*modelv2.OneDropMatrixElement, 0),
	}
	for _, el := range queryResult.Matrix {
		if _, ok := openingStageIds[el.StageID]; !showClosedZones && !ok {
			continue
		}

		stage := stagesMapById[el.StageID]
		if len(stageFilterSet) > 0 {
			if _, ok := stageFilterSet[stage.ArkStageID]; !ok {
				continue
			}
		}

		item := itemsMapById[el.ItemID]
		if len(itemFilterSet) > 0 {
			if _, ok := itemFilterSet[item.ArkItemID]; !ok {
				continue
			}
		}

		// if end time is after now, set it to be null, so that the frontend will show it as "till now"
		var endTime null.Int
		if el.TimeRange.EndTime.After(time.Now()) {
			endTime = null.NewInt(0, false)
		} else {
			endTime = null.NewInt(el.TimeRange.EndTime.UnixMilli(), true)
		}
		oneDropMatrixElement := modelv2.OneDropMatrixElement{
			StageID:   stage.ArkStageID,
			ItemID:    item.ArkItemID,
			Quantity:  el.Quantity,
			Times:     el.Times,
			StdDev:    el.StdDev,
			StartTime: el.TimeRange.StartTime.UnixMilli(),
			EndTime:   endTime,
		}
		results.Matrix = append(results.Matrix, &oneDropMatrixElement)
	}
	return results, nil
}

// filterItemIds returns the item ids that are in itemIdFilter, in their order. Unlike the go-linq filter it replaced,
// it does not write them over itemIds, which may be cached (that never happened, as only a customized time range,
// which reads its item ids from the database, comes with an item filter).
func filterItemIds(itemIds []int, itemIdFilter []int) []int {
	filterSet := make(map[int]struct{}, len(itemIdFilter))
	for _, itemId := range itemIdFilter {
		filterSet[itemId] = struct{}{}
	}
	var kept []int
	for _, itemId := range itemIds {
		if _, ok := filterSet[itemId]; ok {
			kept = append(kept, itemId)
		}
	}
	return kept
}
