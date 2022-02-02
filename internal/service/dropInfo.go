package service

import (
	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type DropInfoService struct {
	DropInfoRepo *repos.DropInfoRepo
	TimeRangeService *TimeRangeService
}

func NewDropInfoService(dropInfoRepo *repos.DropInfoRepo, timeRangeService *TimeRangeService) *DropInfoService {
	return &DropInfoService{
		DropInfoRepo: dropInfoRepo,
		TimeRangeService: timeRangeService,
	}
}

func (s *DropInfoService) GetDropInfosByServer(ctx *fiber.Ctx, server string) ([]*models.DropInfo, error) {
	return s.DropInfoRepo.GetDropInfosByServer(ctx.Context(), server)
}

func (s *DropInfoService) GetMaxAccumulableTimeRangesByServer(ctx *fiber.Ctx, server string) (map[int] map[int] []*models.TimeRange, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx.Context(), server)
	if err != nil {
		return nil, err
	}
	timeRangesMap, err := s.TimeRangeService.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}
	maxAccumulableTimeRanges := make(map[int] map[int] []*models.TimeRange, 0)
	var groupedResults []linq.Group
	linq.From(dropInfos).
		WhereT(func (dropInfo *models.DropInfo) bool { return dropInfo.ItemID.Valid }).
		GroupByT(
			func (dropInfo *models.DropInfo) int { return dropInfo.StageID },
			func (dropInfo *models.DropInfo) *models.DropInfo { return dropInfo },
		).
		ToSlice(&groupedResults)
	for _, el := range groupedResults {
		stageId := el.Key.(int)
		var groupedResults2 []linq.Group
		linq.From(el.Group).
			GroupByT(
				func (dropInfo interface{}) int { return int(dropInfo.(*models.DropInfo).ItemID.Int64) },
				func (dropInfo interface{}) *models.DropInfo { return dropInfo.(*models.DropInfo) },
			).
			ToSlice(&groupedResults2)
		maxAccumulableTimeRangesForOneStage := make(map[int] []*models.TimeRange)
		for _, el := range groupedResults2 {
			itemId := el.Key.(int)
			var sortedDropInfos []*models.DropInfo
			linq.From(el.Group).
				Distinct().
				SortT(
					func (a, b *models.DropInfo) bool { 
						return timeRangesMap[a.RangeID].StartTime.After(*timeRangesMap[b.RangeID].StartTime) 
					}).
				ToSlice(&sortedDropInfos)
			startIdx := len(sortedDropInfos) - 1
			endIdx := 0
			timeRanges := make([]*models.TimeRange, 0)
			for idx, dropInfo := range sortedDropInfos {
				if !dropInfo.Accumulable {
					startIdx = idx
					if idx != 0 {
						startIdx = idx - 1
					}
					break
				}
			}
			for i := endIdx; i <= startIdx; i++ {
				timeRanges = append(timeRanges, timeRangesMap[sortedDropInfos[i].RangeID])
			}
			if len(timeRanges) > 0 {
				maxAccumulableTimeRangesForOneStage[itemId] = timeRanges
			}
		}
		if len(maxAccumulableTimeRangesForOneStage) > 0 {
			maxAccumulableTimeRanges[stageId] = maxAccumulableTimeRangesForOneStage
		}
	}
	return maxAccumulableTimeRanges, nil
} 

func (s *DropInfoService) GetDropInfosWithFilters(ctx *fiber.Ctx, server string, timeRanges []*models.TimeRange, stageIdFilter []int, itemIdFilter []int) ([]*models.DropInfo, error) {
	return s.DropInfoRepo.GetDropInfosWithFilters(ctx.Context(), server, timeRanges, stageIdFilter, itemIdFilter)
}

func (s *DropInfoService) GetItemDropSetByStageIdAndRangeId(ctx *fiber.Ctx, server string, stageId int, rangeId int) ([]int, error) {
	return s.DropInfoRepo.GetItemDropSetByStageIdAndRangeId(ctx.Context(), server, stageId, rangeId)
}

func (s *DropInfoService) GetStageIdsByServer(ctx *fiber.Ctx, server string) ([]int, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx.Context(), server)
	if err != nil {
		return nil, err
	}
	var stageIds []int
	linq.From(dropInfos).SelectT(func (dropInfo *models.DropInfo) int { return dropInfo.StageID }).Distinct().ToSlice(&stageIds)
	return stageIds, nil
}
