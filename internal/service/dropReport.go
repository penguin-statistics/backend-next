package service

import (
	"time"

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

func (s *DropReportService) CalcTotalQuantityForDropMatrix(ctx *fiber.Ctx, server string, timeRange *models.TimeRange, stageIdItemIdMap map[int][]int, accountId *null.Int) ([]*models.TotalQuantityResultForDropMatrix, error) {
	return s.DropReportRepo.CalcTotalQuantityForDropMatrix(ctx.Context(), server, timeRange, stageIdItemIdMap, accountId)	
}

func (s *DropReportService) CalcTotalQuantityForPatternMatrix(ctx *fiber.Ctx, server string, timeRange *models.TimeRange, stageIds []int, accountId *null.Int) ([]*models.TotalQuantityResultForPatternMatrix, error) {
	return s.DropReportRepo.CalcTotalQuantityForPatternMatrix(ctx.Context(), server, timeRange, stageIds, accountId)	
}

func (s *DropReportService) CalcTotalTimesForDropMatrix(ctx *fiber.Ctx, server string, timeRange *models.TimeRange, stageIds []int, accountId *null.Int) ([]*models.TotalTimesResult, error) {
	return s.DropReportRepo.CalcTotalTimes(ctx.Context(), server, timeRange, stageIds, accountId, false)
}

func (s *DropReportService) CalcTotalTimesForPatternMatrix(ctx *fiber.Ctx, server string, timeRange *models.TimeRange, stageIds []int, accountId *null.Int) ([]*models.TotalTimesResult, error) {
	return s.DropReportRepo.CalcTotalTimes(ctx.Context(), server, timeRange, stageIds, accountId, true)
}

func (s *DropReportService) CalcTotalQuantityForTrend(ctx *fiber.Ctx, server string, startTime *time.Time, intervalLength_hrs int, intervalNum int, stageIdItemIdMap map[int][]int, accountId *null.Int) ([]*models.TotalQuantityResultForTrend, error) {
	return s.DropReportRepo.CalcTotalQuantityForTrend(ctx.Context(), server, startTime, intervalLength_hrs, intervalNum, stageIdItemIdMap, accountId)
}

func (s *DropReportService) CalcTotalTimesForTrend(ctx *fiber.Ctx, server string, startTime *time.Time, intervalLength_hrs int, intervalNum int, stageIds []int, accountId *null.Int) ([]*models.TotalTimesResultForTrend, error) {
	return s.DropReportRepo.CalcTotalTimesForTrend(ctx.Context(), server, startTime, intervalLength_hrs, intervalNum, stageIds, accountId)
}
