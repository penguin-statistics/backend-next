package service

import (
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type PatternMatrixElementService struct {
	PatternMatrixElementRepo *repos.PatternMatrixElementRepo
}

func NewPatternMatrixElementService(patternMatrixElementRepo *repos.PatternMatrixElementRepo) *PatternMatrixElementService {
	return &PatternMatrixElementService{
		PatternMatrixElementRepo: patternMatrixElementRepo,
	}
}

func (s *PatternMatrixElementService) BatchSaveElements(ctx *fiber.Ctx, elements []*models.PatternMatrixElement, server string) error {
	return s.PatternMatrixElementRepo.BatchSaveElements(ctx.Context(), elements, server)
}

func (s *PatternMatrixElementService) DeleteByServer(ctx *fiber.Ctx, server string) error {
	return s.PatternMatrixElementRepo.DeleteByServer(ctx.Context(), server)
}

func (s *PatternMatrixElementService) GetElementsByServer(ctx *fiber.Ctx, server string) ([]*models.PatternMatrixElement, error) {
	return s.PatternMatrixElementRepo.GetElementsByServer(ctx.Context(), server)
}
