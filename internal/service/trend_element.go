package service

import (
	"context"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type TrendElementService struct {
	TrendElementRepo *repos.TrendElementRepo
}

func NewTrendElementService(trendElementRepo *repos.TrendElementRepo) *TrendElementService {
	return &TrendElementService{
		TrendElementRepo: trendElementRepo,
	}
}

func (s *TrendElementService) BatchSaveElements(ctx context.Context, elements []*models.TrendElement, server string) error {
	return s.TrendElementRepo.BatchSaveElements(ctx, elements, server)
}

func (s *TrendElementService) DeleteByServer(ctx context.Context, server string) error {
	return s.TrendElementRepo.DeleteByServer(ctx, server)
}

func (s *TrendElementService) GetElementsByServer(ctx context.Context, server string) ([]*models.TrendElement, error) {
	return s.TrendElementRepo.GetElementsByServer(ctx, server)
}
