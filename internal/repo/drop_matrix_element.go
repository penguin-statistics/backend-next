package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
)

type DropMatrixElement struct {
	db *bun.DB
}

func NewDropMatrixElement(db *bun.DB) *DropMatrixElement {
	return &DropMatrixElement{db: db}
}

func (s *DropMatrixElement) BatchSaveElements(ctx context.Context, elements []*models.DropMatrixElement, server string) error {
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

func (s *DropMatrixElement) DeleteByServer(ctx context.Context, server string) error {
	_, err := s.db.NewDelete().Model((*models.DropMatrixElement)(nil)).Where("server = ?", server).Exec(ctx)
	return err
}

func (s *DropMatrixElement) GetElementsByServer(ctx context.Context, server string) ([]*models.DropMatrixElement, error) {
	var elements []*models.DropMatrixElement
	err := s.db.NewSelect().Model(&elements).Where("server = ?", server).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return elements, nil
}
