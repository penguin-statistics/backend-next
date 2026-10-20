package service

import (
	"context"

	"gopkg.in/guregu/null.v3"

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

func (s *DropMatrixElement) GetElementsByServerAndSourceCategoryAndDayNumRange(
	ctx context.Context, server string, sourceCategory string, startDayNum int, endDayNum int,
) ([]*model.DropMatrixElement, error) {
	return s.DropMatrixElementRepo.GetElementsByServerAndSourceCategoryAndDayNumRange(ctx, server, sourceCategory, startDayNum, endDayNum)
}

func (s *DropMatrixElement) IsExistByServerAndDayNum(ctx context.Context, server string, dayNum int) (bool, error) {
	return s.DropMatrixElementRepo.IsExistByServerAndDayNum(ctx, server, dayNum)
}

func (s *DropMatrixElement) CalcGlobalDropMatrixAggregates(
	ctx context.Context, server string, targets []*model.GlobalDropMatrixTarget, sourceCategories []string,
) (map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate, error) {
	return s.DropMatrixElementRepo.CalcGlobalDropMatrixAggregates(ctx, server, targets, sourceCategories)
}

func (s *DropMatrixElement) CalcSiteStatsAggregates(
	ctx context.Context, server string, fromDayNum, toDayNum null.Int,
) ([]*model.SiteStatsAggregate, error) {
	return s.DropMatrixElementRepo.CalcSiteStatsAggregates(ctx, server, fromDayNum, toDayNum)
}
