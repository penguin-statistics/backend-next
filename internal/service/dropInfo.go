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
