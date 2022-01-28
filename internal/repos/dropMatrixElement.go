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

func (s *DropMatrixElementRepo) BatchSaveElements(ctx context.Context, elements []models.DropMatrixElement, server string) error {
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewDelete().Model((*models.DropMatrixElement)(nil)).Where("server = ?", server).Exec(ctx)
		if err != nil {
			return err
		}
		_, err = tx.NewInsert().Model(&elements).Exec(ctx)
		return err
	})
	if err != nil {
		return err
	}
	return nil
}

func (s *DropMatrixElementRepo) DeleteByServer(ctx context.Context, server string) error {
	_, err := s.db.NewDelete().Model((*models.DropMatrixElement)(nil)).Where("server = ?", server).Exec(ctx)
	return err
}
