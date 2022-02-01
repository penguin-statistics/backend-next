package service

import (
	"github.com/gofiber/fiber/v2"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type DropReportService struct {
	DropReportRepo *repos.DropReportRepo
}

func NewDropReportService(dropReportRepo *repos.DropReportRepo) *DropReportService {
	return &DropReportService{
		DropReportRepo: dropReportRepo,
	}
}

func (s *DropReportService) CalcTotalQuantity(ctx *fiber.Ctx, server string, timeRange *models.TimeRange, stageIdItemIdMap map[int][]int, accountId *null.Int) ([]map[string]interface{}, error) {
	return s.DropReportRepo.CalcTotalQuantity(ctx.Context(), server, timeRange, stageIdItemIdMap, accountId)	
}

func (s *DropReportService) CalcTotalTimes(ctx *fiber.Ctx, server string, timeRange *models.TimeRange, stageIds []int, accountId *null.Int) ([]map[string]interface{}, error) {
	return s.DropReportRepo.CalcTotalTimes(ctx.Context(), server, timeRange, stageIds, accountId)
}
