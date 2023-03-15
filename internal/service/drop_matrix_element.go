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

func (s *DropMatrixElement) GetElementsByServerAndSourceCategoryAndStartAndEndTimeAndStageIdAndItemIds(
	ctx context.Context, server string, sourceCategory string, start *time.Time, end *time.Time, stageIdItemIdMap map[int][]int,
) ([]*model.DropMatrixElement, error) {
	return s.DropMatrixElementRepo.GetElementsByServerAndSourceCategoryAndStartAndEndTimeAndStageIdAndItemIds(ctx, server, sourceCategory, start, end, stageIdItemIdMap)
}

func (s *DropMatrixElement) GetElementsByServerAndSourceCategory(ctx context.Context, server string, sourceCategory string) ([]*model.DropMatrixElement, error) {
	return s.DropMatrixElementRepo.GetElementsByServerAndSourceCategory(ctx, server, sourceCategory)
}

func (s *DropMatrixElement) GetElementsByServerAndSourceCategoryAndDayNumRange(
	ctx context.Context, server string, sourceCategory string, startDayNum int, endDayNum int,
) ([]*model.DropMatrixElement, error) {
	return s.DropMatrixElementRepo.GetElementsByServerAndSourceCategoryAndDayNumRange(ctx, server, sourceCategory, startDayNum, endDayNum)
}

func (s *DropMatrixElement) IsExistByServerAndDayNum(ctx context.Context, server string, dayNum int) (bool, error) {
	return s.DropMatrixElementRepo.IsExistByServerAndDayNum(ctx, server, dayNum)
}
