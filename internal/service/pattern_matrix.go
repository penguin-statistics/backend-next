package service

import (
	"context"
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

// Cache: shimGlobalPatternMatrix#server|sourceCategory:{server}|{sourceCategory}, 24hrs, records last modified time
// Called by frontend, used for both global and personal, only for latest timeranges
func (s *PatternMatrix) GetShimPatternMatrix(ctx context.Context, server string, accountId null.Int, sourceCategory string,
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
		slowResults, err := s.applyShimForPatternMatrixQuery(ctx, patternMatrixQueryResult)
		if err != nil {
			return nil, err
		}
		return slowResults, nil
	}

	var results modelv2.PatternMatrixQueryResult
	if !accountId.Valid {
		calculated, err := cache.ShimGlobalPatternMatrix.MutexGetSet(server, &results, valueFunc, 24*time.Hour)
		if err != nil {
			return nil, err
		} else if calculated {
			key := server + constant.CacheSep + sourceCategory
			cache.LastModifiedTime.Set("[shimGlobalPatternMatrix#server|sourceCategory:"+key+"]", time.Now(), 0)
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
		if err := cache.ShimGlobalPatternMatrix.Delete(server + constant.CacheSep + sourceCategory); err != nil {
			return err
		}
		if err := cache.ShimGlobalPatternMatrix.Delete(server + constant.CacheSep + sourceCategory); err != nil {
			return err
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

// Calc global pattern matrix from elements in DB
func (s *PatternMatrix) calcGlobalPatternMatrix(ctx context.Context, server string, sourceCategory string) (*model.PatternMatrixQueryResult, error) {
	patternMatrixQueryResult := &model.PatternMatrixQueryResult{
		PatternMatrix: make([]*model.OnePatternMatrixElement, 0),
	}
	timeRangesMap, err := s.TimeRangeService.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}
	latestTimeRanges, err := s.TimeRangeService.GetLatestTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	stageIdsMap := s.getStageIdsMapByTimeRange(latestTimeRanges)

	patternMatrixElementsMapByStageIdAndPatternID := make(map[int]map[int][]*model.PatternMatrixElement)
	for rangeId, stageIds := range stageIdsMap {
		timeRange := timeRangesMap[rangeId]
		// get elements from DB
		patternMatrixElementsForOneTimeRange, err := s.PatternMatrixElementService.GetElementsByServerAndSourceCategoryAndStartAndEndTimeAndStageIds(
			ctx, server, sourceCategory, timeRange.StartTime, timeRange.EndTime, stageIds)
		if err != nil {
			return nil, err
		}

		// group this one batch by stage id and pattern id
		for _, element := range patternMatrixElementsForOneTimeRange {
			if _, ok := patternMatrixElementsMapByStageIdAndPatternID[element.StageID]; !ok {
				patternMatrixElementsMapByStageIdAndPatternID[element.StageID] = make(map[int][]*model.PatternMatrixElement)
			}
			if _, ok := patternMatrixElementsMapByStageIdAndPatternID[element.StageID][element.PatternID]; !ok {
				patternMatrixElementsMapByStageIdAndPatternID[element.StageID][element.PatternID] = make([]*model.PatternMatrixElement, 0)
			}
			patternMatrixElementsMapByStageIdAndPatternID[element.StageID][element.PatternID] = append(
				patternMatrixElementsMapByStageIdAndPatternID[element.StageID][element.PatternID], element)
		}

		// combine elements for each stage id and pattern id (stage id must show up in stageIdsMap)
		for _, stageId := range stageIds {
			for patternID, elements := range patternMatrixElementsMapByStageIdAndPatternID[stageId] {
				combinedElement := elements[0]
				for _, element := range elements[1:] {
					combinedElement, err = s.combinePatternMatrixElements(combinedElement, element)
					if err != nil {
						return nil, err
					}
				}
				patternMatrixElementsMapByStageIdAndPatternID[stageId][patternID] = []*model.PatternMatrixElement{combinedElement}
			}
		}
	}

	// iterate patternMatrixElementsMapByStageIdAndPatternID to get final result
	for stageId, patternMatrixElementsMapByPatternID := range patternMatrixElementsMapByStageIdAndPatternID {
		for patternID, elements := range patternMatrixElementsMapByPatternID {
			timeRange := &model.TimeRange{
				StartTime: elements[0].StartTime,
				EndTime:   elements[0].EndTime,
			}
			patternMatrixQueryResult.PatternMatrix = append(patternMatrixQueryResult.PatternMatrix, &model.OnePatternMatrixElement{
				StageID:   stageId,
				PatternID: patternID,
				TimeRange: timeRange,
				Times:     elements[0].Times,
				Quantity:  elements[0].Quantity,
			})
		}
	}
	return patternMatrixQueryResult, nil
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
			endTime := null.NewInt(oneDropPattern.TimeRange.EndTime.UnixMilli(), true)
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
			onePatternMatrixElement := modelv2.OnePatternMatrixElement{
				StageID:   stage.ArkStageID,
				Times:     oneDropPattern.Times,
				Quantity:  oneDropPattern.Quantity,
				StartTime: oneDropPattern.TimeRange.StartTime.UnixMilli(),
				EndTime:   endTime,
				Pattern:   &pattern,
			}
			if onePatternMatrixElement.EndTime.Int64 == constant.FakeEndTimeMilli {
				onePatternMatrixElement.EndTime = null.NewInt(0, false)
			}
			results.PatternMatrix = append(results.PatternMatrix, &onePatternMatrixElement)
		}
	}
	return results, nil
}

func (s *PatternMatrix) combinePatternMatrixElements(a, b *model.PatternMatrixElement) (*model.PatternMatrixElement, error) {
	if a.StageID != b.StageID {
		return nil, errors.New("stageId not match")
	}
	if a.Server != b.Server {
		return nil, errors.New("server not match")
	}
	if a.SourceCategory != b.SourceCategory {
		return nil, errors.New("sourceCategory not match")
	}
	if a.PatternID != b.PatternID {
		return nil, errors.New("patternId not match")
	}
	startTime := a.StartTime
	if a.StartTime.After(*b.StartTime) {
		startTime = b.StartTime
	}
	endTime := a.EndTime
	if a.EndTime.Before(*b.EndTime) {
		endTime = b.EndTime
	}
	return &model.PatternMatrixElement{
		StageID:        a.StageID,
		PatternID:      a.PatternID,
		StartTime:      startTime,
		EndTime:        endTime,
		Quantity:       a.Quantity + b.Quantity,
		Times:          a.Times + b.Times,
		Server:         a.Server,
		SourceCategory: a.SourceCategory,
	}, nil
}
