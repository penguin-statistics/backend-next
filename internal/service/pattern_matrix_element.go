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

func (s *PatternMatrixElement) GetAllTimesForGlobalPatternMatrixMapByStageId(ctx context.Context, server string, sourceCategory string) (map[int]*model.AllTimesResultForGlobalPatternMatrix, error) {
	allTimes, err := s.PatternMatrixElementRepo.GetAllTimesForGlobalPatternMatrix(ctx, server, sourceCategory)
	if err != nil {
		return nil, err
	}
	result := make(map[int]*model.AllTimesResultForGlobalPatternMatrix)
	for _, v := range allTimes {
		result[v.StageID] = v
	}
	return result, nil
}

func (s *PatternMatrixElement) GetAllQuantitiesForGlobalPatternMatrixMapByStageIdAndItemId(ctx context.Context, server string, sourceCategory string) (map[int]map[int]*model.AllQuantitiesResultForGlobalPatternMatrix, error) {
	allQuantities, err := s.PatternMatrixElementRepo.GetAllQuantitiesForGlobalPatternMatrix(ctx, server, sourceCategory)
	if err != nil {
		return nil, err
	}
	result := make(map[int]map[int]*model.AllQuantitiesResultForGlobalPatternMatrix)
	for _, v := range allQuantities {
		if _, ok := result[v.StageID]; !ok {
			result[v.StageID] = make(map[int]*model.AllQuantitiesResultForGlobalPatternMatrix)
		}
		result[v.StageID][v.PatternID] = v
	}
	return result, nil
}
