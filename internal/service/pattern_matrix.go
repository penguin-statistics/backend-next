package service

import (
	"context"
	"sort"
	"strconv"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/ahmetb/go-linq/v3"
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
		calculated, err := cache.ShimGlobalPatternMatrix.MutexGetSet(key, &results, valueFunc, 24*time.Hour)
		if err != nil {
			return nil, err
		} else if calculated {
			cache.LastModifiedTime.Set("[shimGlobalPatternMatrix#server|sourceCategory|showAllPatterns:"+key+"]", time.Now(), 0)
		}
		return &results, nil
	} else {
		return valueFunc()
	}
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
	s.PatternMatrixElementService.DeleteByServerAndDayNum(ctx, server, dayNum)

	if len(patternMatrixElements) != 0 {
		s.PatternMatrixElementService.BatchSaveElements(ctx, patternMatrixElements, server)
	}

	// If this is the first time we run the job for this server at this day, we need to update the pattern matrix for the previous day.
	if !exists {
		yesterday := date.Add(time.Hour * -24)
		patternMatrixElementsForYesterday, err := s.calcPatternMatrixByGivenDate(ctx, server, &yesterday, nil, s.Config.MatrixWorkerSourceCategories)
		if err != nil {
			return err
		}
		s.PatternMatrixElementService.DeleteByServerAndDayNum(ctx, server, dayNum-1)
		if len(patternMatrixElementsForYesterday) != 0 {
			s.PatternMatrixElementService.BatchSaveElements(ctx, patternMatrixElementsForYesterday, server)
		}
	}

	// the global pattern matrices are replaced by RefreshGlobalPatternMatrixCaches, not dropped: requests keep
	// getting the previous ones while new ones are calculated
	return nil
}

// RefreshGlobalPatternMatrixCaches recalculates the global pattern matrices of server and replaces the cached ones
// (for every source category, with all and with the top patterns). Requests keep getting the previous ones in the
// meantime instead of all waiting for a recalculation. If the recalculation fails, the cached ones are dropped.
func (s *PatternMatrix) RefreshGlobalPatternMatrixCaches(ctx context.Context, server string) (err error) {
	defer func() {
		if err != nil {
			s.deleteGlobalPatternMatrixCaches(server)
		}
	}()
	for _, sourceCategory := range s.Config.MatrixWorkerSourceCategories {
		global, err := s.calcGlobalPatternMatrix(ctx, server, sourceCategory)
		if err != nil {
			return err
		}
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
			cache.ShimGlobalPatternMatrix.Set(key, *shim, 24*time.Hour)
			cache.LastModifiedTime.Set("[shimGlobalPatternMatrix#server|sourceCategory|showAllPatterns:"+key+"]", time.Now(), 0)
		}
	}
	return nil
}

func (s *PatternMatrix) deleteGlobalPatternMatrixCaches(server string) {
	for _, sourceCategory := range s.Config.MatrixWorkerSourceCategories {
		for _, showAllPatterns := range []string{"true", "false"} {
			cache.ShimGlobalPatternMatrix.Delete(server + constant.CacheSep + sourceCategory + constant.CacheSep + showAllPatterns)
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
	s.PatternMatrixElementService.DeleteByServerAndDayNum(ctx, server, dayNum)
	if len(patternMatrixElements) != 0 {
		s.PatternMatrixElementService.BatchSaveElements(ctx, patternMatrixElements, server)
	}
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
		linq.From(stageIds).WhereT(func(stageId int) bool {
			_, ok := excludeStageIdsSet[stageId]
			return !ok
		}).ToSlice(&stageIds)
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
	finalResult := &model.PatternMatrixQueryResult{
		PatternMatrix: make([]*model.OnePatternMatrixElement, 0),
	}

	latestTimeRanges, err := s.TimeRangeService.GetLatestTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	stageIdsMapByTimeRangeStr := make(map[string][]int, 0)
	for stageId, timeRange := range latestTimeRanges {
		timeRangeStr := timeRange.String()
		if _, ok := stageIdsMapByTimeRangeStr[timeRangeStr]; !ok {
			stageIdsMapByTimeRangeStr[timeRangeStr] = make([]int, 0)
		}
		stageIdsMapByTimeRangeStr[timeRangeStr] = append(stageIdsMapByTimeRangeStr[timeRangeStr], stageId)
	}
	for timeRangeStr, stageIds := range stageIdsMapByTimeRangeStr {
		timeRange := model.TimeRangeFromString(timeRangeStr)

		timesResults, err := s.PatternMatrixElementService.GetAllTimesForGlobalPatternMatrixMapByStageId(ctx, server, timeRange, stageIds, sourceCategory)
		if err != nil {
			return nil, err
		}
		quantityResults, err := s.PatternMatrixElementService.GetAllQuantitiesForGlobalPatternMatrixMapByStageIdAndPatternId(ctx, server, timeRange, stageIds, sourceCategory)
		if err != nil {
			return nil, err
		}
		for _, stageId := range stageIds {
			timesResult, ok := timesResults[stageId]
			if !ok && len(quantityResults[stageId]) > 0 {
				// the elements were rewritten (deleted, then inserted) between the two queries
				return nil, errors.Errorf("pattern matrix of stage %d on %s changed while being read: times missing", stageId, server)
			}
			for patternId, quantityResult := range quantityResults[stageId] {
				onePatternMatrixElement := &model.OnePatternMatrixElement{
					StageID:   stageId,
					PatternID: patternId,
					TimeRange: timeRange,
					Times:     timesResult.Times,
					Quantity:  quantityResult.Quantity,
				}
				finalResult.PatternMatrix = append(finalResult.PatternMatrix, onePatternMatrixElement)
			}
		}
	}
	return finalResult, nil
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
		linq.From(stageIds).WhereT(func(stageId int) bool {
			_, ok := excludeStageIdsSet[stageId]
			return !ok
		}).ToSlice(&stageIds)
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

	var groupedResults []linq.Group
	linq.From(queryResult.PatternMatrix).
		GroupByT(
			func(el *model.OnePatternMatrixElement) int { return el.PatternID },
			func(el *model.OnePatternMatrixElement) *model.OnePatternMatrixElement { return el },
		).ToSlice(&groupedResults)
	for _, group := range groupedResults {
		patternId := group.Key.(int)
		for _, el := range group.Group {
			oneDropPattern := el.(*model.OnePatternMatrixElement)
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
			linq.From(dropPatternElements).SortT(func(el1, el2 *model.DropPatternElement) bool {
				item1 := itemsMapById[el1.ItemID]
				item2 := itemsMapById[el2.ItemID]
				return item1.SortID < item2.SortID
			}).ToSlice(&dropPatternElements)
			for _, dropPatternElement := range dropPatternElements {
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
