package service

import (
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type TrendElementService struct {
	TrendElementRepo *repos.TrendElementRepo
}

func NewTrendElementService(trendElementRepo *repos.TrendElementRepo) *TrendElementService {
	return &TrendElementService{
		TrendElementRepo: trendElementRepo,
	}
}

func (s *TrendElementService) BatchSaveElements(ctx *fiber.Ctx, elements []*models.TrendElement, server string) error {
	return s.TrendElementRepo.BatchSaveElements(ctx.Context(), elements, server)
}

func (s *TrendElementService) DeleteByServer(ctx *fiber.Ctx, server string) error {
	return s.TrendElementRepo.DeleteByServer(ctx.Context(), server)
}

func (s *TrendElementService) GetElementsByServer(ctx *fiber.Ctx, server string) ([]*models.TrendElement, error) {
	return s.TrendElementRepo.GetElementsByServer(ctx.Context(), server)
}
