package service

import (
	"context"
	"time"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo"
)

type DropMatrixElement struct {
	DropMatrixElementRepo *repo.DropMatrixElement
}

func NewDropMatrixElement(dropMatrixElementRepo *repo.DropMatrixElement) *DropMatrixElement {
	return &DropMatrixElement{
		DropMatrixElementRepo: dropMatrixElementRepo,
	}
}

func (s *DropMatrixElement) BatchSaveElements(ctx context.Context, elements []*model.DropMatrixElement, server string) error {
	return s.DropMatrixElementRepo.BatchSaveElements(ctx, elements, server)
}

func (s *DropMatrixElement) DeleteByServerAndDayNum(ctx context.Context, server string, dayNum int) error {
	return s.DropMatrixElementRepo.DeleteByServerAndDayNum(ctx, server, dayNum)
}

func (s *DropMatrixElement) GetElementsByServerAndSourceCategoryAndStartAndEndTime(
	ctx context.Context, server string, sourceCategory string, start *time.Time, end *time.Time,
) ([]*model.DropMatrixElement, error) {
	return s.DropMatrixElementRepo.GetElementsByServerAndSourceCategoryAndStartAndEndTime(ctx, server, sourceCategory, start, end)
}

func (s *DropMatrixElement) GetElementsByServerAndSourceCategory(ctx context.Context, server string, sourceCategory string) ([]*model.DropMatrixElement, error) {
	return s.DropMatrixElementRepo.GetElementsByServerAndSourceCategory(ctx, server, sourceCategory)
}
