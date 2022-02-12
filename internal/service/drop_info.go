package service

import (
	"strconv"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type DropInfoService struct {
	DropInfoRepo     *repos.DropInfoRepo
	TimeRangeService *TimeRangeService
}

func NewDropInfoService(dropInfoRepo *repos.DropInfoRepo, timeRangeService *TimeRangeService) *DropInfoService {
	return &DropInfoService{
		DropInfoRepo:     dropInfoRepo,
		TimeRangeService: timeRangeService,
	}
}

func (s *DropInfoService) GetDropInfosByServer(ctx *fiber.Ctx, server string) ([]*models.DropInfo, error) {
	return s.DropInfoRepo.GetDropInfosByServer(ctx.Context(), server)
}

func (s *DropInfoService) GetDropInfosWithFilters(ctx *fiber.Ctx, server string, timeRanges []*models.TimeRange, stageIdFilter []int, itemIdFilter []int) ([]*models.DropInfo, error) {
	return s.DropInfoRepo.GetDropInfosWithFilters(ctx.Context(), server, timeRanges, stageIdFilter, itemIdFilter)
}

// Cache: itemDropSet#server|stageId|rangeId:{server}|{stageId}|{rangeId}, 24 hrs
func (s *DropInfoService) GetItemDropSetByStageIdAndRangeId(ctx *fiber.Ctx, server string, stageId int, rangeId int) ([]int, error) {
	var itemDropSet []int
	key := server + constants.RedisSeparator + strconv.Itoa(stageId) + constants.RedisSeparator + strconv.Itoa(rangeId)
	err := cache.ItemDropSetByStageIdAndRangeId.Get(key, itemDropSet)
	if err == nil {
		return itemDropSet, nil
	}

	itemDropSet, err = s.DropInfoRepo.GetItemDropSetByStageIdAndRangeId(ctx.Context(), server, stageId, rangeId)
	if err != nil {
		return nil, err
	}

	go cache.ItemDropSetByStageIdAndRangeId.Set(key, itemDropSet, 24*time.Hour)
	return itemDropSet, nil
}

func (s *DropInfoService) GetAppearStageIdsByServer(ctx *fiber.Ctx, server string) ([]int, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx.Context(), server)
	if err != nil {
		return nil, err
	}
	var stageIds []int
	linq.From(dropInfos).SelectT(func(dropInfo *models.DropInfo) int { return dropInfo.StageID }).Distinct().ToSlice(&stageIds)
	return stageIds, nil
}

func (s *DropInfoService) GetCurrentDropInfosByServer(ctx *fiber.Ctx, server string) ([]*models.DropInfo, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx.Context(), server)
	if err != nil {
		return nil, err
	}
	currentTimeRanges, err := s.TimeRangeService.GetCurrentTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	currentTimeRangesMap := make(map[int]*models.TimeRange)
	for _, timeRange := range currentTimeRanges {
		currentTimeRangesMap[timeRange.RangeID] = timeRange
	}
	linq.From(dropInfos).WhereT(func(dropInfo *models.DropInfo) bool {
		return currentTimeRangesMap[dropInfo.RangeID] != nil
	}).ToSlice(&dropInfos)
	return dropInfos, nil
}
