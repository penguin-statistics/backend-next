package service

import (
	"sync"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/utils"
)

type TrendService struct {
	TimeRangeService            *TimeRangeService
	DropReportService           *DropReportService
	DropInfoService             *DropInfoService
	PatternMatrixElementService *PatternMatrixElementService
	DropPatternElementService   *DropPatternElementService
	TrendElementService         *TrendElementService
	DropReportRepo              *repos.DropReportRepo
}

func NewTrendService(
	timeRangeService *TimeRangeService, 
	dropReportService *DropReportService, 
	dropInfoService *DropInfoService,
	patternMatrixElementService *PatternMatrixElementService,
	dropPatternElementService *DropPatternElementService,
	trendElementService *TrendElementService,
	dropReportRepo *repos.DropReportRepo,
) *TrendService {
	return &TrendService{
		TimeRangeService: timeRangeService,
		DropReportService: dropReportService,
		DropInfoService: dropInfoService,
		PatternMatrixElementService: patternMatrixElementService,
		DropPatternElementService: dropPatternElementService,
		TrendElementService: trendElementService,
		DropReportRepo: dropReportRepo,
	}
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
			linq.From(timeRanges).SortT(func (a, b *models.TimeRange) bool {
				return a.StartTime.Before(*b.StartTime) 
			}).ToSlice(&sortedTimeRanges)
			combinedTimeRange := models.TimeRange{
				StartTime: sortedTimeRanges[0].StartTime,
				EndTime: sortedTimeRanges[len(sortedTimeRanges)-1].EndTime,
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
			if diff % 24 != 0 {
				intervalNum++
			}

			if intervalNum > 60 {
				intervalNum = 60
				startTime = endTime.Add(time.Hour * time.Duration((-1) * 24 * (intervalNum + 1)))
			}

			toCalc = append(toCalc, map[string]interface{}{
				"stageId": stageId,
				"itemIds": itemIds,
				"startTime": startTime,
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
		go func(el map[string] interface{}) {
			startTime := el["startTime"].(time.Time)
			intervalNum := el["intervalNum"].(int)
			stageId := el["stageId"].(int)
			itemIds := el["itemIds"].([]int)
			currentBatch, err := s.CalcTrend(ctx, server, &startTime, 24, intervalNum, []int{stageId}, itemIds, &null.Int{})
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

func (s *TrendService) CalcTrend(
	ctx *fiber.Ctx, server string, startTime *time.Time, intervalLength_hrs int, intervalNum int, stageIdFilter []int, itemIdFilter []int, accountId *null.Int) ([]*models.TrendElement, error) {
	endTime := startTime.Add(time.Hour * time.Duration(intervalLength_hrs * intervalNum))
	timeRange := models.TimeRange{
		StartTime: startTime,
		EndTime: &endTime,
	}
	dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(ctx, server, []*models.TimeRange{&timeRange}, stageIdFilter, itemIdFilter)
	if err != nil {
		return nil, err
	}

	quantityResults, err := s.DropReportRepo.CalcTotalQuantityForTrend(ctx.Context(), server, startTime, intervalLength_hrs, intervalNum, utils.GetStageIdItemIdMapFromDropInfos(dropInfos), accountId)
	if err != nil {
		return nil, err
	}
	timesResults, err := s.DropReportRepo.CalcTotalTimesForTrend(ctx.Context(), server, startTime, intervalLength_hrs, intervalNum, utils.GetStageIdsFromDropInfos(dropInfos), accountId)
	if err != nil {
		return nil, err
	}
	combinedResults := s.combineQuantityAndTimesResults(quantityResults, timesResults)
	
	finalResults := make([]*models.TrendElement, 0)
	linq.From(combinedResults).
		SelectT(func (el map[string]interface{}) *models.TrendElement {
			return &models.TrendElement{
				StageID: el["stageId"].(int),
				ItemID: el["itemId"].(int),
				Quantity: el["quantity"].(int),
				Times: el["times"].(int),
				Server: server,
				StartTime: el["startTime"].(*time.Time),
				EndTime: el["endTime"].(*time.Time),
			}
		}).
		ToSlice(&finalResults)
	return finalResults, nil
}

func (s *TrendService) combineQuantityAndTimesResults(quantityResults []map[string]interface{}, timesResults []map[string]interface{}) []map[string]interface{} {
	var firstGroupResultsForQuantity []linq.Group
	combinedResults := make([]map[string]interface{}, 0)
	linq.From(quantityResults).
		GroupByT(
			func(result map[string]interface{}) int { return int(result["group_idx"].(int64)) },
			func(result map[string]interface{}) map[string]interface{} { return result }).
		ToSlice(&firstGroupResultsForQuantity)
	groupResultsMap := make(map[int]map[int]map[int]int)
	for _, firstGroupElements := range firstGroupResultsForQuantity {
		groupId := firstGroupElements.Key.(int)
		var secondGroupResultsForQuantity []linq.Group
		linq.From(firstGroupElements.Group).
			GroupByT(
				func(result map[string]interface{}) int { return int(result["stage_id"].(int64)) },
				func(result map[string]interface{}) map[string]interface{} { return result }).
			ToSlice(&secondGroupResultsForQuantity)
		quantityResultsMap := make(map[int]map[int]int)
		for _, secondGroupElements := range secondGroupResultsForQuantity {
			stageId := secondGroupElements.Key.(int)
			resultsMap := make(map[int]int, 0)
			linq.From(secondGroupElements.Group).
				ToMapByT(&resultsMap,
					func(el interface{}) int { return int(el.(map[string]interface{})["item_id"].(int64)) },
					func(el interface{}) int { return int(el.(map[string]interface{})["total_quantity"].(int64)) })
			quantityResultsMap[stageId] = resultsMap
		}
		groupResultsMap[groupId] = quantityResultsMap
	}

	var firstGroupResultsForTimes []linq.Group
	linq.From(timesResults).
		GroupByT(
			func(result map[string]interface{}) int { return int(result["group_idx"].(int64)) },
			func(result map[string]interface{}) map[string]interface{} { return result }).
		ToSlice(&firstGroupResultsForTimes)
	for _, firstGroupElements := range firstGroupResultsForTimes {
		groupId := firstGroupElements.Key.(int)
		quantityResultsMapForOneGroup := groupResultsMap[groupId]
		var secondGroupResultsForTimes []linq.Group
		linq.From(firstGroupElements.Group).
			GroupByT(
				func(result map[string]interface{}) int { return int(result["stage_id"].(int64)) },
				func(result map[string]interface{}) map[string]interface{} { return result }).
			ToSlice(&secondGroupResultsForTimes)
		for _, secondGroupElements := range secondGroupResultsForTimes {
			stageId := secondGroupElements.Key.(int)
			resultsMap := quantityResultsMapForOneGroup[stageId]
			for _, el := range secondGroupElements.Group {
				times := int(el.(map[string]interface{})["total_times"].(int64))
				for itemId, quantity := range resultsMap {
					startTime := el.(map[string]interface{})["interval_start"].(time.Time)
					endTime := el.(map[string]interface{})["interval_end"].(time.Time)
					combinedResults = append(combinedResults, map[string]interface{}{
						"groupId": groupId,
						"stageId": stageId,
						"itemId":  itemId,
						"times":   times,
						"quantity": quantity,
						"startTime": &startTime,
						"endTime": &endTime,
					})
				}
			}
		}
	}
	return combinedResults
}
