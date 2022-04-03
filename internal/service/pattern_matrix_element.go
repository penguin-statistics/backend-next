package service

import (
	"context"

	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type PatternMatrixElement struct {
	PatternMatrixElementRepo *repo.PatternMatrixElement
}

func NewPatternMatrixElement(patternMatrixElementRepo *repo.PatternMatrixElement) *PatternMatrixElement {
	return &PatternMatrixElement{
		PatternMatrixElementRepo: patternMatrixElementRepo,
	}
}

func (s *PatternMatrixElement) BatchSaveElements(ctx context.Context, elements []*model.PatternMatrixElement, server string) error {
	return s.PatternMatrixElementRepo.BatchSaveElements(ctx, elements, server)
}

func (s *PatternMatrixElement) DeleteByServer(ctx context.Context, server string) error {
	return s.PatternMatrixElementRepo.DeleteByServer(ctx, server)
}

func (s *PatternMatrixElement) GetElementsByServer(ctx context.Context, server string) ([]*model.PatternMatrixElement, error) {
	return s.PatternMatrixElementRepo.GetElementsByServer(ctx, server)
}
