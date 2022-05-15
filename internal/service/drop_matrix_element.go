package service

import (
	"context"

	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/repo"
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

func (s *DropMatrixElement) DeleteByServer(ctx context.Context, server string) error {
	return s.DropMatrixElementRepo.DeleteByServer(ctx, server)
}

func (s *DropMatrixElement) GetElementsByServerAndSourceCategory(ctx context.Context, server string, sourceCategory string) ([]*model.DropMatrixElement, error) {
	return s.DropMatrixElementRepo.GetElementsByServerAndSourceCategory(ctx, server, sourceCategory)
}
