package service

import (
	"context"
	"strconv"
	"time"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/repo"
)

type DropPatternElement struct {
	DropPatternElementRepo *repo.DropPatternElement
}

func NewDropPatternElement(dropPatternElementRepo *repo.DropPatternElement) *DropPatternElement {
	return &DropPatternElement{
		DropPatternElementRepo: dropPatternElementRepo,
	}
}

// Cache: dropPatternElements#patternId:{patternId}, 24hrs
func (s *DropPatternElement) GetDropPatternElementsByPatternId(ctx context.Context, patternId int) ([]*model.DropPatternElement, error) {
	var dropPatternElements []*model.DropPatternElement
	err := cache.DropPatternElementsByPatternID.Get(strconv.Itoa(patternId), &dropPatternElements)
	if err == nil {
		return dropPatternElements, nil
	}

	dbDropPatternElements, err := s.DropPatternElementRepo.GetDropPatternElementsByPatternId(ctx, patternId)
	if err != nil {
		return nil, err
	}
	cache.DropPatternElementsByPatternID.Set(strconv.Itoa(patternId), dbDropPatternElements, 24*time.Hour)
	return dbDropPatternElements, nil
}

func (s *DropPatternElement) GetDropPatternElementsMapByPatternIds(ctx context.Context, patternIds []int) (map[int][]*model.DropPatternElement, error) {
	elements, err := s.DropPatternElementRepo.GetDropPatternElementsByPatternIds(ctx, patternIds)
	if err != nil {
		return nil, err
	}
	elementsMap := make(map[int][]*model.DropPatternElement)
	for _, element := range elements {
		if _, ok := elementsMap[element.DropPatternID]; !ok {
			elementsMap[element.DropPatternID] = make([]*model.DropPatternElement, 0)
		}
		elementsMap[element.DropPatternID] = append(elementsMap[element.DropPatternID], element)
	}
	return elementsMap, nil
}
