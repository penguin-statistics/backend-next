package service

import (
	"context"

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

func (s *DropMatrixElementService) BatchSaveElements(ctx context.Context, elements []*models.DropMatrixElement, server string) error {
	return s.DropMatrixElementRepo.BatchSaveElements(ctx, elements, server)
}

func (s *DropMatrixElementService) DeleteByServer(ctx context.Context, server string) error {
	return s.DropMatrixElementRepo.DeleteByServer(ctx, server)
}

func (s *DropMatrixElementService) GetElementsByServer(ctx context.Context, server string) ([]*models.DropMatrixElement, error) {
	return s.DropMatrixElementRepo.GetElementsByServer(ctx, server)
}
