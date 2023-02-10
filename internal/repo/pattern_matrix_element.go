package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo/selector"
)

type PatternMatrixElement struct {
	db  *bun.DB
	sel selector.S[model.PatternMatrixElement]
}

func NewPatternMatrixElement(db *bun.DB) *PatternMatrixElement {
	return &PatternMatrixElement{db: db, sel: selector.New[model.PatternMatrixElement](db)}
}

func (r *PatternMatrixElement) BatchSaveElements(ctx context.Context, elements []*model.PatternMatrixElement, server string) error {
	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewDelete().Model((*model.PatternMatrixElement)(nil)).Where("server = ?", server).Exec(ctx)
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

func (r *PatternMatrixElement) DeleteByServer(ctx context.Context, server string) error {
	_, err := r.db.NewDelete().Model((*model.PatternMatrixElement)(nil)).Where("server = ?", server).Exec(ctx)
	return err
}

func (r *PatternMatrixElement) GetElementsByServerAndSourceCategory(ctx context.Context, server string, sourceCategory string) ([]*model.PatternMatrixElement, error) {
	var elements []*model.PatternMatrixElement
	err := r.db.NewSelect().Model(&elements).Where("server = ?", server).Where("source_category = ?", sourceCategory).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return elements, nil
}
