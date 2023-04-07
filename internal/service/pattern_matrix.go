package service

import (
	"context"
	"strconv"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/ahmetb/go-linq/v3"
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
	date := time.Now()
	endTime := time.Now()
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

	for _, sourceCategory := range s.Config.MatrixWorkerSourceCategories {
		for _, showAllPatterns := range []bool{true, false} {
			key := server + constant.CacheSep + sourceCategory + constant.CacheSep + strconv.FormatBool(showAllPatterns)
			if err := cache.ShimGlobalPatternMatrix.Delete(key); err != nil {
				return err
			}
			if err := cache.ShimGlobalPatternMatrix.Delete(key); err != nil {
				return err
			}
		}
	}
	return nil
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
	stageIdsMap := s.getStageIdsMapByTimeRange(allTimeRanges)
	elements := make([]*model.PatternMatrixElement, 0)
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
		for _, sourceCategory := range sourceCategories {
			currentBatch, err := s.calcPatternMatrixForTimeRanges(ctx, server, []*model.TimeRange{intersection}, stageIds, null.NewInt(0, false), sourceCategory)
			if err != nil {
				return nil, err
			}
			elements = append(elements, currentBatch...)
		}
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
			timesResult := timesResults[stageId]
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
		currentBatch, err := s.calcPatternMatrixForTimeRanges(ctx, server, timeRanges, stageIds, accountId, sourceCategory)
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

// Called by both global and personal
func (s *PatternMatrix) calcPatternMatrixForTimeRanges(
	ctx context.Context, server string, timeRanges []*model.TimeRange, stageIdFilter []int, accountId null.Int, sourceCategory string,
) ([]*model.PatternMatrixElement, error) {
	results := make([]*model.PatternMatrixElement, 0)

	// For one time range whose end time is FakeEndTimeMilli, we will make separate query to get times and quantity.
	// We need to make sure they are queried based on the same set of drop reports. So we will use a unified end time instead of FakeEndTimeMilli.
	unifiedEndTime := time.Now()
	for _, timeRange := range timeRanges {
		if timeRange.EndTime.After(unifiedEndTime) {
			timeRange.EndTime = &unifiedEndTime
		}
	}

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
			SourceCategory:     sourceCategory,
			ExcludeNonOneTimes: true,
		}
		quantityResults, err := s.DropReportService.CalcTotalQuantityForPatternMatrix(ctx, queryCtx)
		if err != nil {
			return nil, err
		}
		timesResults, err := s.DropReportService.CalcTotalTimesForPatternMatrix(ctx, queryCtx)
		if err != nil {
			return nil, err
		}
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
	return results, nil
}

func (s *PatternMatrix) combineQuantityAndTimesResults(
	quantityResults []*model.TotalQuantityResultForPatternMatrix, timesResults []*model.TotalTimesResult,
) []*model.CombinedResultForDropPattern {
	var firstGroupResults []linq.Group
	combinedResults := make([]*model.CombinedResultForDropPattern, 0)
	linq.From(quantityResults).
		GroupByT(
			func(result *model.TotalQuantityResultForPatternMatrix) int { return result.StageID },
			func(result *model.TotalQuantityResultForPatternMatrix) *model.TotalQuantityResultForPatternMatrix {
				return result
			}).
		ToSlice(&firstGroupResults)
	quantityResultsMap := make(map[int]map[int]int)
	for _, firstGroupElements := range firstGroupResults {
		stageId := firstGroupElements.Key.(int)
		resultsMap := make(map[int]int)
		linq.From(firstGroupElements.Group).
			ToMapByT(&resultsMap,
				func(el any) int { return el.(*model.TotalQuantityResultForPatternMatrix).PatternID },
				func(el any) int { return el.(*model.TotalQuantityResultForPatternMatrix).TotalQuantity })
		quantityResultsMap[stageId] = resultsMap
	}

	var secondGroupResults []linq.Group
	linq.From(timesResults).
		GroupByT(
			func(result *model.TotalTimesResult) int { return result.StageID },
			func(result *model.TotalTimesResult) *model.TotalTimesResult { return result }).
		ToSlice(&secondGroupResults)
	for _, secondGroupResults := range secondGroupResults {
		stageId := secondGroupResults.Key.(int)
		quantityResultsMapForOneStage := quantityResultsMap[stageId]
		for _, el := range secondGroupResults.Group {
			times := el.(*model.TotalTimesResult).TotalTimes
			for patternId, quantity := range quantityResultsMapForOneStage {
				combinedResults = append(combinedResults, &model.CombinedResultForDropPattern{
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
	// sort all elements by times desc, and limit the number of elements
	for stageId, elements := range elementsMapByStageId {
		linq.From(elements).OrderByDescendingT(func(el *model.OnePatternMatrixElement) int { return el.Times }).ToSlice(&elements)
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
