package service

import (
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type DropMatrixElementService struct {
	DropMatrixElementRepo *repos.DropMatrixElementRepo
}

func NewDropMatrixElementService(dropMatrixElementRepo *repos.DropMatrixElementRepo) *DropMatrixElementService {
	return &DropMatrixElementService{
		DropMatrixElementRepo: dropMatrixElementRepo,
	}
}

func (s *DropMatrixElementService) BatchSaveElements(ctx *fiber.Ctx, elements []*models.DropMatrixElement, server string) error {
	return s.DropMatrixElementRepo.BatchSaveElements(ctx.Context(), elements, server)
}

func (s *DropMatrixElementService) DeleteByServer(ctx *fiber.Ctx, server string) error {
	return s.DropMatrixElementRepo.DeleteByServer(ctx.Context(), server)
}

func (s *DropMatrixElementService) GetElementsByServer(ctx *fiber.Ctx, server string) ([]*models.DropMatrixElement, error) {
	return s.DropMatrixElementRepo.GetElementsByServer(ctx.Context(), server)
}
