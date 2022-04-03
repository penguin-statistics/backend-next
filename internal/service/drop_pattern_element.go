package service

import (
	"context"
	"strconv"
	"time"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type DropPatternElementService struct {
	DropPatternElementRepo *repo.DropPatternElementRepo
}

func NewDropPatternElementService(dropPatternElementRepo *repo.DropPatternElementRepo) *DropPatternElementService {
	return &DropPatternElementService{
		DropPatternElementRepo: dropPatternElementRepo,
	}
}

// Cache: dropPatternElements#patternId:{patternId}, 24hrs
func (s *DropPatternElementService) GetDropPatternElementsByPatternId(ctx context.Context, patternId int) ([]*models.DropPatternElement, error) {
	var dropPatternElements []*models.DropPatternElement
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
