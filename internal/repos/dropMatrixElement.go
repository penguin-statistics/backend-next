package repos

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
)

type DropMatrixElementRepo struct {
	db *bun.DB
}

func NewDropMatrixElementRepo(db *bun.DB) *DropMatrixElementRepo {
	return &DropMatrixElementRepo{db: db}
}

func (s *DropMatrixElementRepo) BatchSaveElements(ctx context.Context, elements []models.DropMatrixElement) {
	s.db.NewInsert().Model(elements).Exec(ctx)
}

func (s *DropMatrixElementRepo) DeleteByServer(ctx context.Context, server string) {
	s.db.NewDelete().Model((*models.DropMatrixElement)(nil)).Where("server = ?", server).Exec(ctx)
}
