package service

import (
	"context"
	"strconv"
	"time"

	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type DropPatternElementService struct {
	DropPatternElementRepo *repo.DropPatternElement
}

func NewDropPatternElementService(dropPatternElementRepo *repo.DropPatternElement) *DropPatternElementService {
	return &DropPatternElementService{
		DropPatternElementRepo: dropPatternElementRepo,
	}
}

// Cache: dropPatternElements#patternId:{patternId}, 24hrs
func (s *DropPatternElementService) GetDropPatternElementsByPatternId(ctx context.Context, patternId int) ([]*model.DropPatternElement, error) {
	var dropPatternElements []*model.DropPatternElement
	err := cache.DropPatternElementsByPatternID.Get(strconv.Itoa(patternId), &dropPatternElements)
	if err == nil {
		return dropPatternElements, nil
	}

	dbDropPatternElements, err := s.DropPatternElementRepo.GetDropPatternElementsByPatternId(ctx, patternId)
	if err != nil {
		return nil, err
	}
	go cache.DropPatternElementsByPatternID.Set(strconv.Itoa(patternId), dbDropPatternElements, 24*time.Hour)
	return dbDropPatternElements, nil
}
