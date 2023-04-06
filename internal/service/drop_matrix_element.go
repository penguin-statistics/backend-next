package service

import (
	"context"

	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
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

func (s *DropMatrixElement) GetAllTimesForGlobalDropMatrixMapByStageIdAndItemId(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIds []int, sourceCategory string,
) (map[int]map[int]*model.AllTimesResultForGlobalDropMatrix, error) {
	allTimes, err := s.DropMatrixElementRepo.GetAllTimesForGlobalDropMatrix(ctx, server, timeRange, stageIds, sourceCategory)
	if err != nil {
		return nil, err
	}
	result := make(map[int]map[int]*model.AllTimesResultForGlobalDropMatrix)
	for _, v := range allTimes {
		if _, ok := result[v.StageID]; !ok {
			result[v.StageID] = make(map[int]*model.AllTimesResultForGlobalDropMatrix)
		}
		result[v.StageID][v.ItemID] = v
	}
	return result, nil
}

func (s *DropMatrixElement) GetAllQuantitiesForGlobalDropMatrixMapByStageIdAndItemId(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIds []int, sourceCategory string,
) (map[int]map[int]*model.AllQuantitiesResultForGlobalDropMatrix, error) {
	allQuantities, err := s.DropMatrixElementRepo.GetAllQuantitiesForGlobalDropMatrix(ctx, server, timeRange, stageIds, sourceCategory)
	if err != nil {
		return nil, err
	}
	result := make(map[int]map[int]*model.AllQuantitiesResultForGlobalDropMatrix)
	for _, v := range allQuantities {
		if _, ok := result[v.StageID]; !ok {
			result[v.StageID] = make(map[int]*model.AllQuantitiesResultForGlobalDropMatrix)
		}
		result[v.StageID][v.ItemID] = v
	}
	return result, nil
}

func (s *DropMatrixElement) GetAllQuantityBucketsForGlobalDropMatrixMapByStageIdAndItemId(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIds []int, sourceCategory string,
) (map[int]map[int]*model.AllQuantityBucketsResultForGlobalDropMatrix, error) {
	allQuantityBuckets, err := s.DropMatrixElementRepo.GetAllQuantityBucketsForGlobalDropMatrix(ctx, server, timeRange, stageIds, sourceCategory)
	if err != nil {
		return nil, err
	}
	result := make(map[int]map[int]*model.AllQuantityBucketsResultForGlobalDropMatrix)
	for _, v := range allQuantityBuckets {
		if _, ok := result[v.StageID]; !ok {
			result[v.StageID] = make(map[int]*model.AllQuantityBucketsResultForGlobalDropMatrix)
		}
		result[v.StageID][v.ItemID] = v
	}
	return result, nil
}

func (s *DropMatrixElement) CalcTotalItemQuantityForShimSiteStats(ctx context.Context, server string) ([]*modelv2.TotalItemQuantity, error) {
	return s.DropMatrixElementRepo.CalcTotalItemQuantityForShimSiteStats(ctx, server)
}

func (s *DropMatrixElement) CalcTotalStageQuantityForShimSiteStats(ctx context.Context, server string) ([]*modelv2.TotalStageTime, error) {
	return s.DropMatrixElementRepo.CalcTotalStageQuantityForShimSiteStats(ctx, server)
}

func (s *DropMatrixElement) CalcTotalSanityCostForShimSiteStats(ctx context.Context, server string) (sanity int, err error) {
	return s.DropMatrixElementRepo.CalcTotalSanityCostForShimSiteStats(ctx, server)
}
