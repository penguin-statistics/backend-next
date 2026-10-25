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

func patternMatrixElementDay(el *model.PatternMatrixElement) int { return el.DayNum }

func (s *PatternMatrixElement) ReplaceDay(ctx context.Context, server string, dayNum int, elements []*model.PatternMatrixElement) error {
	return s.PatternMatrixElementRepo.ReplaceDay(ctx, server, dayNum, elements)
}

func (s *PatternMatrixElement) IsExistByServerAndDayNum(ctx context.Context, server string, dayNum int) (bool, error) {
	return s.PatternMatrixElementRepo.IsExistByServerAndDayNum(ctx, server, dayNum)
}

func (s *PatternMatrixElement) CalcGlobalPatternMatrixAggregates(
	ctx context.Context, server string, targets []*model.GlobalPatternMatrixTarget, sourceCategories []string,
) (map[string]*model.GlobalPatternMatrixAggregates, error) {
	return s.PatternMatrixElementRepo.CalcGlobalPatternMatrixAggregates(ctx, server, targets, sourceCategories)
}
