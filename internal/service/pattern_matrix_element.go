package service

import (
	"context"
	"time"

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

func (s *PatternMatrixElement) DeleteByServer(ctx context.Context, server string) error {
	return s.PatternMatrixElementRepo.DeleteByServer(ctx, server)
}

func (s *PatternMatrixElement) DeleteByServerAndDayNum(ctx context.Context, server string, dayNum int) error {
	return s.PatternMatrixElementRepo.DeleteByServerAndDayNum(ctx, server, dayNum)
}

func (s *PatternMatrixElement) GetElementsByServerAndSourceCategoryAndStartAndEndTimeAndStageIds(
	ctx context.Context, server string, sourceCategory string, start *time.Time, end *time.Time, stageIds []int,
) ([]*model.PatternMatrixElement, error) {
	return s.PatternMatrixElementRepo.GetElementsByServerAndSourceCategoryAndStartAndEndTimeAndStageIds(ctx, server, sourceCategory, start, end, stageIds)
}

func (s *PatternMatrixElement) GetElementsByServerAndSourceCategory(ctx context.Context, server string, sourceCategory string) ([]*model.PatternMatrixElement, error) {
	return s.PatternMatrixElementRepo.GetElementsByServerAndSourceCategory(ctx, server, sourceCategory)
}

func (s *PatternMatrixElement) GetElementsByServerAndSourceCategoryAndDayNumRange(
	ctx context.Context, server string, sourceCategory string, startDayNum int, endDayNum int,
) ([]*model.PatternMatrixElement, error) {
	return s.PatternMatrixElementRepo.GetElementsByServerAndSourceCategoryAndDayNumRange(ctx, server, sourceCategory, startDayNum, endDayNum)
}

func (s *PatternMatrixElement) IsExistByServerAndDayNum(ctx context.Context, server string, dayNum int) (bool, error) {
	return s.PatternMatrixElementRepo.IsExistByServerAndDayNum(ctx, server, dayNum)
}
