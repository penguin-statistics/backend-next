package service

import (
	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type TimeRangeService struct {
	TimeRangeRepo *repos.TimeRangeRepo
}

func NewTimeRangeService(timeRangeRepo *repos.TimeRangeRepo) *TimeRangeService {
	return &TimeRangeService{
		TimeRangeRepo: timeRangeRepo,
	}
}

func (s *TimeRangeService) GetAllTimeRangesByServer(ctx *fiber.Ctx, server string) ([]*models.TimeRange, error) {
	timeRanges, err := s.TimeRangeRepo.GetTimeRangesByServer(ctx.Context(), server)
	if err != nil {
		return nil, err
	}
	return timeRanges, nil
}

func (s *TimeRangeService) GetTimeRangesMap(ctx *fiber.Ctx, server string) (map[int]*models.TimeRange, error) {
	timeRanges, err := s.TimeRangeRepo.GetTimeRangesByServer(ctx.Context(), server)
	if err != nil {
		return nil, err
	}
	timeRangesMap := make(map[int]*models.TimeRange)
	linq.From(timeRanges).
		ToMapByT(
			&timeRangesMap,
			func(timeRange *models.TimeRange) int { return timeRange.RangeID },
			func(timeRange *models.TimeRange) *models.TimeRange { return timeRange })
	return timeRangesMap, nil
}
