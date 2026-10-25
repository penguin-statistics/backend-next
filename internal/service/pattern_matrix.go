package service

import (
	"context"
	"sort"
	"strconv"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/util"
)

type PatternMatrix struct {
	Config                      *appconfig.Config
	TimeRangeService            *TimeRange
	DropReportService           *DropReport
	DropInfoService             *DropInfo
	PatternMatrixElementService *PatternMatrixElement
	DropPatternElementService   *DropPatternElement
	StageService                *Stage
	ItemService                 *Item
}

func NewPatternMatrix(
	config *appconfig.Config,
	timeRangeService *TimeRange,
	dropReportService *DropReport,
	dropInfoService *DropInfo,
	patternMatrixElementService *PatternMatrixElement,
	dropPatternElementService *DropPatternElement,
	stageService *Stage,
	itemService *Item,
) *PatternMatrix {
	return &PatternMatrix{
		Config:                      config,
		TimeRangeService:            timeRangeService,
		DropReportService:           dropReportService,
		DropInfoService:             dropInfoService,
		PatternMatrixElementService: patternMatrixElementService,
		DropPatternElementService:   dropPatternElementService,
		StageService:                stageService,
		ItemService:                 itemService,
	}
}

// =========== Global & Personal, Latest Timeranges ===========

// Cache: shimGlobalPatternMatrix#server|sourceCategory|showAllPatterns:{server}|{sourceCategory}|{showAllPatterns}, 24hrs, records last modified time
// Called by frontend, used for both global and personal, only for latest timeranges
func (s *PatternMatrix) GetShimPatternMatrix(ctx context.Context, server string, accountId null.Int, sourceCategory string, showAllPatterns bool,
) (*modelv2.PatternMatrixQueryResult, error) {
	valueFunc := func() (*modelv2.PatternMatrixQueryResult, error) {
		var patternMatrixQueryResult *model.PatternMatrixQueryResult
		var err error
		if accountId.Valid {
			patternMatrixQueryResult, err = s.getLatestPatternMatrixResults(ctx, server, accountId, sourceCategory)
		} else {
			patternMatrixQueryResult, err = s.calcGlobalPatternMatrix(ctx, server, sourceCategory)
		}
		if err != nil {
			return nil, err
		}
		if !showAllPatterns {
			newPatternMatrix, err := s.interceptPatternMatrixResults(patternMatrixQueryResult.PatternMatrix, s.Config.PatternMatrixLimit)
			if err != nil {
				return nil, err
			}
			patternMatrixQueryResult.PatternMatrix = newPatternMatrix
		}
		slowResults, err := s.applyShimForPatternMatrixQuery(ctx, patternMatrixQueryResult)
		if err != nil {
			return nil, err
		}
		return slowResults, nil
	}

	var results modelv2.PatternMatrixQueryResult
	if !accountId.Valid {
		key := server + constant.CacheSep + sourceCategory + constant.CacheSep + strconv.FormatBool(showAllPatterns)
		publish := cache.PublishingLastModifiedTime[modelv2.PatternMatrixQueryResult]("[shimGlobalPatternMatrix#server|sourceCategory|showAllPatterns:" + key + "]")
		if _, err := cache.ShimGlobalPatternMatrix.MutexGetSetPublishing(key, &results, valueFunc, 24*time.Hour, publish); err != nil {
			return nil, err
		}
		return &results, nil
	} else {
		return valueFunc()
	}
}

// WarmUpGlobalPatternMatrixCaches fills the cached global pattern matrices of server (for every source category, with
// all and with the top patterns) as requests do: it skips those cached, and waits for a calculation a request runs
// instead of running another one. RefreshGlobalPatternMatrixCaches recalculates them instead.
func (s *PatternMatrix) WarmUpGlobalPatternMatrixCaches(ctx context.Context, server string) error {
	for _, sourceCategory := range s.Config.MatrixWorkerSourceCategories {
		for _, showAllPatterns := range []bool{true, false} {
			if _, err := s.GetShimPatternMatrix(ctx, server, null.Int{}, sourceCategory, showAllPatterns); err != nil {
				return err
			}
		}
	}
	return nil
}

// =========== Global ===========

// Calc today's pattern matrix elements and save to DB
// Called by worker
func (s *PatternMatrix) RunCalcPatternMatrixJob(ctx context.Context, server string) error {
	date := util.Now()
	endTime := util.Now()
	patternMatrixElements, err := s.calcPatternMatrixByGivenDate(ctx, server, &date, &endTime, s.Config.MatrixWorkerSourceCategories)
	if err != nil {
		return err
	}
	dayNum := util.GetDayNum(&date, server)
	exists, err := s.PatternMatrixElementService.IsExistByServerAndDayNum(ctx, server, dayNum)
	if err != nil {
		return err
	}
	patternMatrixElements = elementsOfDay(patternMatrixElements, dayNum, patternMatrixElementDay)
	if err := s.PatternMatrixElementService.ReplaceDay(ctx, server, dayNum, patternMatrixElements); err != nil {
		return err
	}
	describeCalcJob(ctx, dayNum, s.Config.MatrixWorkerSourceCategories, len(patternMatrixElements))

	// If this is the first time we run the job for this server at this day, we need to update the pattern matrix for the previous day.
	if !exists {
		yesterday := date.Add(time.Hour * -24)
		patternMatrixElementsForYesterday, err := s.calcPatternMatrixByGivenDate(ctx, server, &yesterday, nil, s.Config.MatrixWorkerSourceCategories)
		if err != nil {
			return err
		}
		patternMatrixElementsForYesterday = elementsOfDay(patternMatrixElementsForYesterday, dayNum-1, patternMatrixElementDay)
		if err := s.PatternMatrixElementService.ReplaceDay(ctx, server, dayNum-1, patternMatrixElementsForYesterday); err != nil {
			return err
		}
		describePreviousDay(ctx, len(patternMatrixElementsForYesterday))
	}

	// the global pattern matrices are replaced by RefreshGlobalPatternMatrixCaches, not dropped: requests keep
	// getting the previous ones while new ones are calculated
	return nil
}

// RefreshGlobalPatternMatrixCaches recalculates the global pattern matrices of server and replaces the cached ones
// (for every source category, with all and with the top patterns). Requests keep getting the previous ones in the
// meantime instead of all waiting for a recalculation. If the recalculation fails, the cached ones are dropped, also
// if it panics (the worker recovers the panic): the source categories cached before it must not keep matrices of
// this refresh while the others keep those of an earlier one.
func (s *PatternMatrix) RefreshGlobalPatternMatrixCaches(ctx context.Context, server string) error {
	refreshed := false
	defer func() {
		if !refreshed {
			s.deleteGlobalPatternMatrixCaches(server)
		}
	}()
	globals, err := s.calcGlobalPatternMatrices(ctx, server, s.Config.MatrixWorkerSourceCategories)
	if err != nil {
		return err
	}
	for _, sourceCategory := range s.Config.MatrixWorkerSourceCategories {
		global := globals[sourceCategory]
		shims := make(map[bool]*modelv2.PatternMatrixQueryResult, 2)
		for _, showAllPatterns := range []bool{true, false} {
			result := *global
			if !showAllPatterns {
				if result.PatternMatrix, err = s.interceptPatternMatrixResults(global.PatternMatrix, s.Config.PatternMatrixLimit); err != nil {
					return err
				}
			}
			if shims[showAllPatterns], err = s.applyShimForPatternMatrixQuery(ctx, &result); err != nil {
				return err
			}
		}
		for showAllPatterns, shim := range shims {
			key := server + constant.CacheSep + sourceCategory + constant.CacheSep + strconv.FormatBool(showAllPatterns)
			cache.SetLastModifiedTime("[shimGlobalPatternMatrix#server|sourceCategory|showAllPatterns:"+key+"]", *shim, func() {
				cache.ShimGlobalPatternMatrix.Set(key, *shim, 24*time.Hour)
			})
		}
	}
	refreshed = true
	return nil
}

func (s *PatternMatrix) deleteGlobalPatternMatrixCaches(server string) {
	for _, sourceCategory := range s.Config.MatrixWorkerSourceCategories {
		for _, showAllPatterns := range []string{"true", "false"} {
			cache.ShimGlobalPatternMatrix.Delete(server + constant.CacheSep + sourceCategory + constant.CacheSep + showAllPatterns)
		}
	}
}

// GlobalPatternMatrixCachesUsed reports whether any of the global pattern matrices of server that
// RefreshGlobalPatternMatrixCaches replaces is used (see cache.Set.Used): whether this process serves them, and should
// follow their refreshes (cachesync).
func (s *PatternMatrix) GlobalPatternMatrixCachesUsed(server string) bool {
	for _, sourceCategory := range s.Config.MatrixWorkerSourceCategories {
		for _, showAllPatterns := range []string{"true", "false"} {
			if cache.ShimGlobalPatternMatrix.Used(server + constant.CacheSep + sourceCategory + constant.CacheSep + showAllPatterns) {
				return true
			}
		}
	}
	return false
}

// DropGlobalPatternMatrixCaches deletes the cached global pattern matrices of server that
// RefreshGlobalPatternMatrixCaches replaces, those cached only: a refresh of them in another process left them stale,
// and requests recalculate them.
func (s *PatternMatrix) DropGlobalPatternMatrixCaches(server string) {
	for _, sourceCategory := range s.Config.MatrixWorkerSourceCategories {
		for _, showAllPatterns := range []string{"true", "false"} {
			deleteCached(cache.ShimGlobalPatternMatrix, server+constant.CacheSep+sourceCategory+constant.CacheSep+showAllPatterns)
		}
	}
}

// Update pattern matrix elements for a given date (entire day)
// Called by admin api
func (s *PatternMatrix) UpdatePatternMatrixByGivenDate(ctx context.Context, server string, date *time.Time) error {
	patternMatrixElements, err := s.calcPatternMatrixByGivenDate(ctx, server, date, nil, s.Config.MatrixWorkerSourceCategories)
	if err != nil {
		return err
	}
	dayNum := util.GetDayNum(date, server)
	patternMatrixElements = elementsOfDay(patternMatrixElements, dayNum, patternMatrixElementDay)
	if err := s.PatternMatrixElementService.ReplaceDay(ctx, server, dayNum, patternMatrixElements); err != nil {
		return err
	}
	describeCalcJob(ctx, dayNum, s.Config.MatrixWorkerSourceCategories, len(patternMatrixElements))
	return nil
}

func (s *PatternMatrix) calcPatternMatrixByGivenDate(
	ctx context.Context, server string, date *time.Time, endTime *time.Time, sourceCategories []string,
) ([]*model.PatternMatrixElement, error) {
	start := time.UnixMilli(util.GetDayStartTime(date, server))
	startNextDay := start.Add(time.Hour * 24)
	end := lo.Ternary(endTime == nil, &startNextDay, endTime)
	timeRangeGiven := &model.TimeRange{
		StartTime: &start,
		EndTime:   end,
	}

	timeRangesMap, err := s.TimeRangeService.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}
	allTimeRanges, err := s.TimeRangeService.GetLatestTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	excludeStageIdsSet, err := s.getExcludeStageIdsSet(ctx)
	if err != nil {
		return nil, err
	}
	// Many ranges intersect the given day in the very same interval (usually the whole day), so stages are
	// grouped by intersection and every interval is calculated with a single query.
	stageIdsMap := s.getStageIdsMapByTimeRange(allTimeRanges)
	intersections := make(map[string]*model.TimeRange)
	stageIdsByIntersection := make(map[string][]int)
	for rangeId, stageIds := range stageIdsMap {
		timeRange := timeRangesMap[rangeId]
		intersection := util.GetIntersection(timeRange, timeRangeGiven)
		if intersection == nil {
			continue
		}

		// exclude some stages (gachabox, recruit) before calc
		stageIds = excludeStageIds(stageIds, excludeStageIdsSet)
		if len(stageIds) == 0 {
			continue
		}
		key := intersection.String()
		intersections[key] = intersection
		stageIdsByIntersection[key] = append(stageIdsByIntersection[key], stageIds...)
	}

	elements := make([]*model.PatternMatrixElement, 0)
	for key, stageIds := range stageIdsByIntersection {
		currentBatch, err := s.calcPatternMatrixForTimeRanges(ctx, server, []*model.TimeRange{intersections[key]}, stageIds, null.NewInt(0, false), sourceCategories)
		if err != nil {
			return nil, err
		}
		elements = append(elements, currentBatch...)
	}
	return elements, nil
}

func (s *PatternMatrix) calcGlobalPatternMatrix(ctx context.Context, server string, sourceCategory string) (*model.PatternMatrixQueryResult, error) {
	results, err := s.calcGlobalPatternMatrices(ctx, server, []string{sourceCategory})
	if err != nil {
		return nil, err
	}
	return results[sourceCategory], nil
}

// calcGlobalPatternMatrices calculates the global pattern matrices of server for every source category: each stage
// over its latest time range, from a single statement per source category.
func (s *PatternMatrix) calcGlobalPatternMatrices(ctx context.Context, server string, sourceCategories []string) (map[string]*model.PatternMatrixQueryResult, error) {
	latestTimeRanges, err := s.TimeRangeService.GetLatestTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	targets := make([]*model.GlobalPatternMatrixTarget, 0, len(latestTimeRanges))
	for stageId, timeRange := range latestTimeRanges {
		// as the elements had it: start and end only, at second precision
		targets = append(targets, &model.GlobalPatternMatrixTarget{StageID: stageId, TimeRange: model.TimeRangeFromString(timeRange.String())})
	}
	// a statement per source category, as for the global drop matrices
	aggregates := make(map[string]*model.GlobalPatternMatrixAggregates, len(sourceCategories))
	for _, sourceCategory := range sourceCategories {
		categoryAggregates, err := s.PatternMatrixElementService.CalcGlobalPatternMatrixAggregates(ctx, server, targets, []string{sourceCategory})
		if err != nil {
			return nil, err
		}
		aggregates[sourceCategory] = categoryAggregates[sourceCategory]
	}

	results := make(map[string]*model.PatternMatrixQueryResult, len(sourceCategories))
	for _, sourceCategory := range sourceCategories {
		aggregate := aggregates[sourceCategory]
		result := &model.PatternMatrixQueryResult{
			PatternMatrix: make([]*model.OnePatternMatrixElement, 0),
		}
		for _, target := range targets {
			quantities := aggregate.QuantitiesByStageID[target.StageID]
			times, ok := aggregate.TimesByStageID[target.StageID]
			if !ok && len(quantities) > 0 {
				// an element with quantity > 0 has times > 0
				return nil, errors.Errorf("pattern matrix of stage %d on %s has quantities but no times", target.StageID, server)
			}
			for patternId, quantity := range quantities {
				result.PatternMatrix = append(result.PatternMatrix, &model.OnePatternMatrixElement{
					StageID:   target.StageID,
					PatternID: patternId,
					TimeRange: target.TimeRange,
					Times:     times,
					Quantity:  quantity,
				})
			}
		}
		results[sourceCategory] = result
	}
	return results, nil
}

// =========== Personal ===========

func (s *PatternMatrix) getLatestPatternMatrixResults(ctx context.Context, server string, accountId null.Int, sourceCategory string) (*model.PatternMatrixQueryResult, error) {
	patternMatrixElements, err := s.getLatestPatternMatrixElements(ctx, server, accountId, sourceCategory)
	if err != nil {
		return nil, err
	}
	return s.convertPatternMatrixElementsToDropPatternQueryResult(ctx, server, patternMatrixElements)
}

func (s *PatternMatrix) getLatestPatternMatrixElements(ctx context.Context, server string, accountId null.Int, sourceCategory string) ([]*model.PatternMatrixElement, error) {
	timeRangesMap, err := s.TimeRangeService.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}
	allTimeRanges, err := s.TimeRangeService.GetLatestTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	excludeStageIdsSet, err := s.getExcludeStageIdsSet(ctx)
	if err != nil {
		return nil, err
	}

	stageIdsMap := s.getStageIdsMapByTimeRange(allTimeRanges)
	elements := make([]*model.PatternMatrixElement, 0)
	for rangeId, stageIds := range stageIdsMap {
		// exclude some stages (gachabox, recruit) before calc
		stageIds = excludeStageIds(stageIds, excludeStageIdsSet)
		if len(stageIds) == 0 {
			continue
		}

		timeRanges := []*model.TimeRange{timeRangesMap[rangeId]}
		currentBatch, err := s.calcPatternMatrixForTimeRanges(ctx, server, timeRanges, stageIds, accountId, []string{sourceCategory})
		if err != nil {
			return nil, err
		}
		elements = append(elements, currentBatch...)
	}
	return elements, nil
}

func (s *PatternMatrix) convertPatternMatrixElementsToDropPatternQueryResult(ctx context.Context, server string, patternMatrixElements []*model.PatternMatrixElement) (*model.PatternMatrixQueryResult, error) {
	timeRangesMap, err := s.TimeRangeService.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}

	result := &model.PatternMatrixQueryResult{
		PatternMatrix: make([]*model.OnePatternMatrixElement, 0),
	}
	for _, patternMatrixElement := range patternMatrixElements {
		timeRange := timeRangesMap[patternMatrixElement.RangeID]
		result.PatternMatrix = append(result.PatternMatrix, &model.OnePatternMatrixElement{
			StageID:   patternMatrixElement.StageID,
			PatternID: patternMatrixElement.PatternID,
			Quantity:  patternMatrixElement.Quantity,
			Times:     patternMatrixElement.Times,
			TimeRange: timeRange,
		})
	}
	return result, nil
}

// =========== Helpers ===========

// Called by both global and personal. Elements are returned grouped by time range, then by source category
// in the given order.
func (s *PatternMatrix) calcPatternMatrixForTimeRanges(
	ctx context.Context, server string, timeRanges []*model.TimeRange, stageIdFilter []int, accountId null.Int, sourceCategories []string,
) ([]*model.PatternMatrixElement, error) {
	results := make([]*model.PatternMatrixElement, 0)

	dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, timeRanges, stageIdFilter, nil)
	if err != nil {
		return nil, err
	}

	stageIds := util.GetStageIdsFromDropInfos(dropInfos)
	stageItemFilter := make(map[int][]int, 0)
	for _, stageId := range stageIds {
		stageItemFilter[stageId] = make([]int, 0)
	}
	for _, timeRange := range timeRanges {
		queryCtx := &model.DropReportQueryContext{
			Server:             server,
			StartTime:          timeRange.StartTime,
			EndTime:            timeRange.EndTime,
			AccountID:          accountId,
			StageItemFilter:    &stageItemFilter,
			ExcludeNonOneTimes: true,
		}
		quantityResultsBySourceCategory, err := s.DropReportService.CalcTotalQuantityForPatternMatrixBySourceCategories(ctx, queryCtx, sourceCategories)
		if err != nil {
			return nil, err
		}
		for _, sourceCategory := range sourceCategories {
			quantityResults := quantityResultsBySourceCategory[sourceCategory]
			timesResults := calcTotalTimesFromPatternQuantities(quantityResults)
			combinedResults := s.combineQuantityAndTimesResults(quantityResults, timesResults)
			for _, result := range combinedResults {
				results = append(results, &model.PatternMatrixElement{
					StageID:        result.StageID,
					PatternID:      result.PatternID,
					StartTime:      queryCtx.StartTime,
					EndTime:        queryCtx.EndTime,
					DayNum:         util.GetDayNum(queryCtx.StartTime, queryCtx.Server),
					Quantity:       result.Quantity,
					Times:          result.Times,
					Server:         server,
					SourceCategory: sourceCategory,
					RangeID:        timeRange.RangeID,
				})
			}
		}
	}
	return results, nil
}

func (s *PatternMatrix) combineQuantityAndTimesResults(
	quantityResults []*model.TotalQuantityResultForPatternMatrix, timesResults []*model.TotalTimesResult,
) []*model.CombinedResultForDropPattern {
	combinedResults := make([]*model.CombinedResultForDropPattern, 0)
	quantityResultsMap := make(map[int]map[int]int)
	for _, result := range quantityResults {
		if _, ok := quantityResultsMap[result.StageID]; !ok {
			quantityResultsMap[result.StageID] = make(map[int]int)
		}
		quantityResultsMap[result.StageID][result.PatternID] = result.TotalQuantity
	}

	for _, timesResult := range timesResults {
		for patternId, quantity := range quantityResultsMap[timesResult.StageID] {
			combinedResults = append(combinedResults, &model.CombinedResultForDropPattern{
				StageID:   timesResult.StageID,
				PatternID: patternId,
				Quantity:  quantity,
				Times:     timesResult.TotalTimes,
			})
		}
	}
	return combinedResults
}

// Pattern quantities only count reports with times = 1, so a stage's total times is the sum of its
// pattern counts; this saves a second scan of drop_reports with the very same filters.
func calcTotalTimesFromPatternQuantities(quantityResults []*model.TotalQuantityResultForPatternMatrix) []*model.TotalTimesResult {
	timesByStageId := make(map[int]int)
	stageIds := make([]int, 0)
	for _, result := range quantityResults {
		if _, ok := timesByStageId[result.StageID]; !ok {
			stageIds = append(stageIds, result.StageID)
		}
		timesByStageId[result.StageID] += result.TotalQuantity
	}
	results := make([]*model.TotalTimesResult, 0, len(stageIds))
	for _, stageId := range stageIds {
		results = append(results, &model.TotalTimesResult{StageID: stageId, TotalTimes: timesByStageId[stageId]})
	}
	return results
}

func (s *PatternMatrix) getStageIdsMapByTimeRange(timeRangesMap map[int]*model.TimeRange) map[int][]int {
	results := make(map[int][]int)
	for stageId, timeRange := range timeRangesMap {
		if _, ok := results[timeRange.RangeID]; !ok {
			results[timeRange.RangeID] = make([]int, 0)
		}
		results[timeRange.RangeID] = append(results[timeRange.RangeID], stageId)
	}
	return results
}

func (s *PatternMatrix) getExcludeStageIdsSet(ctx context.Context) (map[int]struct{}, error) {
	excludeStageIdsSet := make(map[int]struct{}, 0)
	// exclude gacha box stages
	gachaboxStages, err := s.StageService.GetGachaBoxStages(ctx)
	if err != nil {
		return nil, err
	}
	for _, stage := range gachaboxStages {
		excludeStageIdsSet[stage.StageID] = struct{}{}
	}
	// exclude recruit stage
	stagesMapByArkId, err := s.StageService.GetStagesMapByArkId(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := stagesMapByArkId[constant.RecruitStageID]; ok {
		excludeStageIdsSet[stagesMapByArkId[constant.RecruitStageID].StageID] = struct{}{}
	}
	return excludeStageIdsSet, nil
}

func (s *PatternMatrix) applyShimForPatternMatrixQuery(ctx context.Context, queryResult *model.PatternMatrixQueryResult) (*modelv2.PatternMatrixQueryResult, error) {
	results := &modelv2.PatternMatrixQueryResult{
		PatternMatrix: make([]*modelv2.OnePatternMatrixElement, 0),
	}

	itemsMapById, err := s.ItemService.GetItemsMapById(ctx)
	if err != nil {
		return nil, err
	}

	stagesMapById, err := s.StageService.GetStagesMapById(ctx)
	if err != nil {
		return nil, err
	}

	// grouped by pattern id, in the order the pattern ids first appear
	for _, group := range util.GroupBy(queryResult.PatternMatrix, func(el *model.OnePatternMatrixElement) int { return el.PatternID }) {
		patternId := group.Key
		for _, oneDropPattern := range group.Items {
			stage := stagesMapById[oneDropPattern.StageID]
			dropPatternElements, err := s.DropPatternElementService.GetDropPatternElementsByPatternId(ctx, patternId)
			if err != nil {
				return nil, err
			}
			// create pattern object from dropPatternElements
			pattern := modelv2.Pattern{
				PatternID: patternId,
				Drops:     make([]*modelv2.OneDrop, 0),
			}
			// sorted as go-linq sorted them: a private copy, written back into dropPatternElements (which may be the
			// cached slice, shared with concurrent requests) only once sorted, and only the copy is read below
			sorted := append([]*model.DropPatternElement(nil), dropPatternElements...)
			sort.Sort(dropPatternElementsBySortID{elements: sorted, itemsMapById: itemsMapById})
			copy(dropPatternElements, sorted)
			for _, dropPatternElement := range sorted {
				item := itemsMapById[dropPatternElement.ItemID]
				pattern.Drops = append(pattern.Drops, &modelv2.OneDrop{
					ItemID:   item.ArkItemID,
					Quantity: dropPatternElement.Quantity,
				})
			}

			// if end time is after now, set it to be null, so that the frontend will show it as "till now"
			var endTime null.Int
			if oneDropPattern.TimeRange.EndTime.After(time.Now()) {
				endTime = null.NewInt(0, false)
			} else {
				endTime = null.NewInt(oneDropPattern.TimeRange.EndTime.UnixMilli(), true)
			}
			onePatternMatrixElement := modelv2.OnePatternMatrixElement{
				StageID:   stage.ArkStageID,
				Times:     oneDropPattern.Times,
				Quantity:  oneDropPattern.Quantity,
				StartTime: oneDropPattern.TimeRange.StartTime.UnixMilli(),
				EndTime:   endTime,
				Pattern:   &pattern,
			}
			results.PatternMatrix = append(results.PatternMatrix, &onePatternMatrixElement)
		}
	}
	return results, nil
}

func (s *PatternMatrix) interceptPatternMatrixResults(onePatternMatrixElements []*model.OnePatternMatrixElement, limit int) ([]*model.OnePatternMatrixElement, error) {
	elementsMapByStageId := make(map[int][]*model.OnePatternMatrixElement)
	for _, onePatternMatrixElement := range onePatternMatrixElements {
		if _, ok := elementsMapByStageId[onePatternMatrixElement.StageID]; !ok {
			elementsMapByStageId[onePatternMatrixElement.StageID] = make([]*model.OnePatternMatrixElement, 0)
		}
		elementsMapByStageId[onePatternMatrixElement.StageID] = append(elementsMapByStageId[onePatternMatrixElement.StageID], onePatternMatrixElement)
	}
	// keep the most frequent patterns of each stage: by quantity (times is the stage's, the same for all its patterns),
	// then by pattern id, so that the same patterns are kept every time
	for stageId, elements := range elementsMapByStageId {
		sort.Slice(elements, func(i, j int) bool {
			if elements[i].Quantity != elements[j].Quantity {
				return elements[i].Quantity > elements[j].Quantity
			}
			return elements[i].PatternID < elements[j].PatternID
		})
		if len(elements) > limit {
			elementsMapByStageId[stageId] = elements[:limit]
		}
	}
	results := make([]*model.OnePatternMatrixElement, 0)
	for _, elements := range elementsMapByStageId {
		results = append(results, elements...)
	}
	return results, nil
}

// excludeStageIds filters stageIds in place, keeping the stages that are not in excludeStageIdsSet.
func excludeStageIds(stageIds []int, excludeStageIdsSet map[int]struct{}) []int {
	kept := stageIds[:0]
	for _, stageId := range stageIds {
		if _, ok := excludeStageIdsSet[stageId]; !ok {
			kept = append(kept, stageId)
		}
	}
	return kept
}

// dropPatternElementsBySortID sorts drop pattern elements by the sort id of their items.
type dropPatternElementsBySortID struct {
	elements     []*model.DropPatternElement
	itemsMapById map[int]*model.Item
}

func (s dropPatternElementsBySortID) Len() int { return len(s.elements) }
func (s dropPatternElementsBySortID) Swap(i, j int) {
	s.elements[i], s.elements[j] = s.elements[j], s.elements[i]
}
func (s dropPatternElementsBySortID) Less(i, j int) bool {
	return s.itemsMapById[s.elements[i].ItemID].SortID < s.itemsMapById[s.elements[j].ItemID].SortID
}
