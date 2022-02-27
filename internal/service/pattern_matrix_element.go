package service

import (
	"context"

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

func (s *PatternMatrixElementService) BatchSaveElements(ctx context.Context, elements []*models.PatternMatrixElement, server string) error {
	return s.PatternMatrixElementRepo.BatchSaveElements(ctx, elements, server)
}

func (s *PatternMatrixElementService) DeleteByServer(ctx context.Context, server string) error {
	return s.PatternMatrixElementRepo.DeleteByServer(ctx, server)
}

func (s *PatternMatrixElementService) GetElementsByServer(ctx context.Context, server string) ([]*models.PatternMatrixElement, error) {
	return s.PatternMatrixElementRepo.GetElementsByServer(ctx, server)
}
