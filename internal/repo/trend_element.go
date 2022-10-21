package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
)

type TrendElement struct {
	db *bun.DB
}

func NewTrendElement(db *bun.DB) *TrendElement {
	return &TrendElement{db: db}
}

func (s *TrendElement) BatchSaveElements(ctx context.Context, elements []*model.TrendElement, server string) error {
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewDelete().Model((*model.TrendElement)(nil)).Where("server = ?", server).Exec(ctx)
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

func (s *TrendElement) DeleteByServer(ctx context.Context, server string) error {
	_, err := s.db.NewDelete().Model((*model.TrendElement)(nil)).Where("server = ?", server).Exec(ctx)
	return err
}

func (s *TrendElement) GetElementsByServerAndSourceCategory(ctx context.Context, server string, sourceCategory string) ([]*model.TrendElement, error) {
	var elements []*model.TrendElement
	err := s.db.NewSelect().Model(&elements).Where("server = ?", server).Where("source_category = ?", sourceCategory).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return elements, nil
}
