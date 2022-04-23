package service

import (
	"context"

	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type TrendElement struct {
	TrendElementRepo *repo.TrendElement
}

func NewTrendElement(trendElementRepo *repo.TrendElement) *TrendElement {
	return &TrendElement{
		TrendElementRepo: trendElementRepo,
	}
}

func (s *TrendElement) BatchSaveElements(ctx context.Context, elements []*model.TrendElement, server string) error {
	return s.TrendElementRepo.BatchSaveElements(ctx, elements, server)
}

func (s *TrendElement) DeleteByServer(ctx context.Context, server string) error {
	return s.TrendElementRepo.DeleteByServer(ctx, server)
}

func (s *TrendElement) GetElementsByServer(ctx context.Context, server string) ([]*model.TrendElement, error) {
	return s.TrendElementRepo.GetElementsByServer(ctx, server)
}
