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

func (s *DropInfoService) GetMaxAccumulableTimeRangesByStageId(ctx *fiber.Ctx, server string, stageId int) (map[int] *models.TimeRange, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByStageId(ctx.Context(), stageId)
	if err != nil {
		return nil, err
	}
	timeRangesMap, err := s.TimeRangeService.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}
	var groupedResults []linq.Group
	linq.From(dropInfos).
		WhereT(func (dropInfo *models.DropInfo) bool { return dropInfo.ItemID.Valid }).
		GroupByT(
			func (dropInfo *models.DropInfo) int { return int(dropInfo.ItemID.Int64) },
			func (dropInfo *models.DropInfo) *models.DropInfo { return dropInfo },
		).
		ToSlice(&groupedResults)
	maxAccumulableTimeRanges := make(map[int] *models.TimeRange)
	for _, el := range groupedResults {
		itemId := el.Key.(int)
		var sortedDropInfos []*models.DropInfo
		linq.From(el.Group).
			Distinct().
			SortT(
				func (a, b *models.DropInfo) bool { 
					return timeRangesMap[a.RangeID].StartTime.After(*timeRangesMap[b.RangeID].StartTime) 
				}).
			ToSlice(&sortedDropInfos)
		startDropInfo := sortedDropInfos[len(sortedDropInfos) - 1]
		endDropInfo := sortedDropInfos[0]
		for idx, dropInfo := range sortedDropInfos {
			if !dropInfo.Accumulable {
				targetStartIdx := idx
				if idx != 0 {
					targetStartIdx = idx - 1
				}
				startDropInfo = sortedDropInfos[targetStartIdx]
				break
			}
		}
		maxAccumulableTimeRanges[itemId] = &models.TimeRange{
			StartTime: timeRangesMap[startDropInfo.RangeID].StartTime, 
			EndTime: timeRangesMap[endDropInfo.RangeID].EndTime,
		}
	}
	return maxAccumulableTimeRanges, nil
}

func (s *DropInfoService) GetDropInfosWithFilters(ctx *fiber.Ctx, server string, rangeIds []int, stageIdFilter []int, itemIdFilter []int) ([]*models.DropInfo, error) {
	return s.DropInfoRepo.GetDropInfosWithFilters(ctx.Context(), server, rangeIds, stageIdFilter, itemIdFilter)
}

func (s *DropInfoService) GetItemDropSetByStageIdAndRangeId(ctx *fiber.Ctx, server string, stageId int, rangeId int) ([]int, error) {
	return s.DropInfoRepo.GetItemDropSetByStageIdAndRangeId(ctx.Context(), server, stageId, rangeId)
}
