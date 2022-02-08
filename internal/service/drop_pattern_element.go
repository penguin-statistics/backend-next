package service

import (
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type DropPatternElementService struct {
	DropPatternElementRepo *repos.DropPatternElementRepo
}

func NewDropPatternElementService(dropPatternElementRepo *repos.DropPatternElementRepo) *DropPatternElementService {
	return &DropPatternElementService{
		DropPatternElementRepo: dropPatternElementRepo,
	}
}

func (s *DropPatternElementService) GetDropPatternElementsByPatternId(ctx *fiber.Ctx, patternId int) ([]*models.DropPatternElement, error) {
	return s.DropPatternElementRepo.GetDropPatternElementsByPatternId(ctx.Context(), patternId)
}
