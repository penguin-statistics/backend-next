package service

import (
	"context"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo"
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

func (s *PatternMatrixElement) DeleteByServerAndDayNum(ctx context.Context, server string, dayNum int) error {
	return s.PatternMatrixElementRepo.DeleteByServerAndDayNum(ctx, server, dayNum)
}

func (s *PatternMatrixElement) IsExistByServerAndDayNum(ctx context.Context, server string, dayNum int) (bool, error) {
	return s.PatternMatrixElementRepo.IsExistByServerAndDayNum(ctx, server, dayNum)
}

func (s *PatternMatrixElement) CalcGlobalPatternMatrixAggregates(
	ctx context.Context, server string, targets []*model.GlobalPatternMatrixTarget, sourceCategories []string,
) (map[string]*model.GlobalPatternMatrixAggregates, error) {
	return s.PatternMatrixElementRepo.CalcGlobalPatternMatrixAggregates(ctx, server, targets, sourceCategories)
}
