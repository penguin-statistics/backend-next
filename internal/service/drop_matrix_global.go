package service

import (
	"context"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/util"
)

type DropMatrixGlobal struct {
	Config                   *appconfig.Config
	TimeRangeService         *TimeRange
	DropReportService        *DropReport
	DropInfoService          *DropInfo
	DropMatrixElementService *DropMatrixElement
	StageService             *Stage
	ItemService              *Item
}

func NewDropMatrixGlobal(
	config *appconfig.Config,
	timeRangeService *TimeRange,
	dropReportService *DropReport,
	dropInfoService *DropInfo,
	dropMatrixElementService *DropMatrixElement,
	stageService *Stage,
	itemService *Item,
) *DropMatrixGlobal {
	return &DropMatrixGlobal{
		Config:                   config,
		TimeRangeService:         timeRangeService,
		DropReportService:        dropReportService,
		DropInfoService:          dropInfoService,
		DropMatrixElementService: dropMatrixElementService,
		StageService:             stageService,
		ItemService:              itemService,
	}
}

// Update drop matrix elements for a given date (entire day)
func (s *DropMatrixGlobal) UpdateDropMatrixByGivenDate(ctx context.Context, server string, date *time.Time) error {
	dropMatrixElements, err := s.calcDropMatrixByGivenDate(ctx, server, date, nil, s.Config.MatrixWorkerSourceCategories)
	if err != nil {
		return err
	}
	dayNum := util.GetDayNum(date, server)
	s.DropMatrixElementService.DeleteByServerAndDayNum(ctx, server, dayNum)
	if len(dropMatrixElements) != 0 {
		s.DropMatrixElementService.BatchSaveElements(ctx, dropMatrixElements, server)
	}
	return nil
}

func (s *DropMatrixGlobal) RunCalcDropMatrixJob(ctx context.Context, server string) error {
	date := time.Now()
	endTime := time.Now()
	dropMatrixElements, err := s.calcDropMatrixByGivenDate(ctx, server, &date, &endTime, s.Config.MatrixWorkerSourceCategories)
	if err != nil {
		return err
	}
	dayNum := util.GetDayNum(&date, server)
	exists, err := s.DropMatrixElementService.DropMatrixElementRepo.IsExistByServerAndDayNum(ctx, server, dayNum)
	if err != nil {
		return err
	}
	s.DropMatrixElementService.DeleteByServerAndDayNum(ctx, server, dayNum)

	if len(dropMatrixElements) != 0 {
		s.DropMatrixElementService.BatchSaveElements(ctx, dropMatrixElements, server)
	}

	// If this is the first time we run the job for this server at this day, we need to update the drop matrix for the previous day.
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
	}
	return nil
}

/**
 * Calculate drop matrix for a given date
 * @param date indicates the date to calculate drop matrix
 * @param endTime if nil, the calculation will be done for the entire day; otherwise, the calculation will be done for the partial day
 */
func (s *DropMatrixGlobal) calcDropMatrixByGivenDate(
	ctx context.Context, server string, date *time.Time, endTime *time.Time, sourceCategories []string) ([]*model.DropMatrixElement, error) {
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

	for timeRangeStr, stageIdsItemIdsMap := range stageIdsItemIdsMapByTimeRangeStr {
		timeRange := model.TimeRangeFromString(timeRangeStr)
		for _, sourceCategory := range sourceCategories {
			queryCtx := &model.DropReportQueryContext{
				Server:             server,
				StartTime:          timeRange.StartTime,
				EndTime:            timeRange.EndTime,
				SourceCategory:     sourceCategory,
				ExcludeNonOneTimes: true,
				StageItemFilter:    &stageIdsItemIdsMap,
			}

			log.Debug().
				Str("timeRange", timeRange.HumanReadableString(server)).
				Str("server", server).
				Str("sourceCategory", sourceCategory).
				Msg("Calculate drop matrix")
			for stageId, itemIds := range stageIdsItemIdsMap {
				log.Debug().
					Int("stageId", stageId).
					Ints("itemIds", itemIds).
					Msg("StageId and itemIds")
			}

			res, err := s.calcDropMatrix(ctx, queryCtx)
			if err != nil {
				return nil, err
			}
			dropMatrixElements = append(dropMatrixElements, res...)
		}
	}
	return dropMatrixElements, nil
}

func (s *DropMatrixGlobal) calcDropMatrix(ctx context.Context, queryCtx *model.DropReportQueryContext) ([]*model.DropMatrixElement, error) {
	var combinedResults []*model.CombinedResultForDropMatrix
	quantityResults, err := s.DropReportService.CalcTotalQuantityForDropMatrix(ctx, queryCtx)
	if err != nil {
		return nil, err
	}
	timesResults, err := s.DropReportService.CalcTotalTimesForDropMatrix(ctx, queryCtx)
	if err != nil {
		return nil, err
	}
	quantityUniqCountResults, err := s.DropReportService.CalcQuantityUniqCount(ctx, queryCtx)
	if err != nil {
		return nil, err
	}

	oneBatch := util.CombineQuantityAndTimesResults(quantityResults, timesResults, quantityUniqCountResults, nil)
	combinedResults = append(combinedResults, oneBatch...)

	// save stage times for later use
	stageTimesMap := map[int]int{}

	// grouping results by stage id
	var groupedResults []linq.Group
	linq.From(combinedResults).
		GroupByT(
			func(el *model.CombinedResultForDropMatrix) int { return el.StageID },
			func(el *model.CombinedResultForDropMatrix) *model.CombinedResultForDropMatrix { return el }).ToSlice(&groupedResults)

	dropMatrixElements := make([]*model.DropMatrixElement, 0)
	for _, el := range groupedResults {
		stageId := el.Key.(int)
		itemIds := (*queryCtx.StageItemFilter)[stageId]

		// get all item ids which are dropped in this stage, save in dropSet
		timeRange := &model.TimeRange{
			StartTime: queryCtx.StartTime,
			EndTime:   queryCtx.EndTime,
		}
		dropInfos, err := s.DropInfoService.GetDropInfosWithFilters(
			ctx, queryCtx.Server, []*model.TimeRange{timeRange}, []int{stageId}, itemIds)
		if err != nil {
			return nil, err
		}
		var dropItemIds []int
		linq.From(dropInfos).
			WhereT(func(el *model.DropInfo) bool { return el.ItemID.Valid }).
			SelectT(func(el *model.DropInfo) int { return int(el.ItemID.Int64) }).
			ToSlice(&dropItemIds)
		linq.From(dropItemIds).WhereT(func(itemId int) bool { return linq.From(itemIds).Contains(itemId) }).ToSlice(&dropItemIds)
		// use a fake hashset to save item ids
		dropSet := make(map[int]struct{}, len(dropItemIds))
		for _, itemId := range dropItemIds {
			dropSet[itemId] = struct{}{}
		}

		for _, el2 := range el.Group {
			itemId := el2.(*model.CombinedResultForDropMatrix).ItemID
			quantity := el2.(*model.CombinedResultForDropMatrix).Quantity
			times := el2.(*model.CombinedResultForDropMatrix).Times
			quantityBuckets := el2.(*model.CombinedResultForDropMatrix).QuantityBuckets
			dropMatrixElement := model.DropMatrixElement{
				StageID:         stageId,
				ItemID:          itemId,
				Quantity:        quantity,
				QuantityBuckets: quantityBuckets,
				Times:           times,
				Server:          queryCtx.Server,
				SourceCategory:  queryCtx.SourceCategory,
				StartTime:       queryCtx.StartTime,
				EndTime:         queryCtx.EndTime,
				DayNum:          util.GetDayNum(queryCtx.StartTime, queryCtx.Server),
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
				DayNum:          util.GetDayNum(queryCtx.StartTime, queryCtx.Server),
			}
			dropMatrixElements = append(dropMatrixElements, &dropMatrixElementWithZeroQuantity)
		}
	}
	return dropMatrixElements, nil
}
