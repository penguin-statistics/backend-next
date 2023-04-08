package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
)

type DropPatternElement struct {
	DB *bun.DB
}

func NewDropPatternElement(db *bun.DB) *DropPatternElement {
	return &DropPatternElement{DB: db}
}

func (r *DropPatternElement) GetDropPatternElements(ctx context.Context) ([]*model.DropPatternElement, error) {
	var elements []*model.DropPatternElement
	err := r.DB.NewSelect().
		Model(&elements).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return elements, nil
}

func (r *DropPatternElement) GetDropPatternElementById(ctx context.Context, id int) (*model.DropPatternElement, error) {
	var DropPatternElement model.DropPatternElement
	err := r.DB.NewSelect().
		Model(&DropPatternElement).
		Where("id = ?", id).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &DropPatternElement, nil
}

func (r *DropPatternElement) GetDropPatternElementByHash(ctx context.Context, hash string) (*model.DropPatternElement, error) {
	var DropPatternElement model.DropPatternElement
	err := r.DB.NewSelect().
		Model(&DropPatternElement).
		Where("hash = ?", hash).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &DropPatternElement, nil
}

func (r *DropPatternElement) CreateDropPatternElements(ctx context.Context, tx bun.Tx, patternId int, drops []*types.Drop) ([]*model.DropPatternElement, error) {
	elements := make([]model.DropPatternElement, 0, len(drops))
	for _, drop := range drops {
		element := model.DropPatternElement{
			ItemID:        drop.ItemID,
			Quantity:      drop.Quantity,
			DropPatternID: patternId,
		}
		elements = append(elements, element)
	}

	_, err := tx.NewInsert().
		Model(&elements).
		Exec(ctx)
	if err != nil {
		return nil, err
	}

	ptrElements := make([]*model.DropPatternElement, 0, len(elements))
	for i := range elements {
		ptrElements = append(ptrElements, &elements[i])
	}

	return ptrElements, nil
}

func (r *DropPatternElement) GetDropPatternElementsByPatternId(ctx context.Context, patternId int) ([]*model.DropPatternElement, error) {
	var elements []*model.DropPatternElement
	err := r.DB.NewSelect().
		Model(&elements).
		Where("drop_pattern_id = ?", patternId).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		elements = make([]*model.DropPatternElement, 0)
	} else if err != nil {
		return nil, err
	}
	return elements, nil
}

func (r *DropPatternElement) GetDropPatternElementsByPatternIds(ctx context.Context, patternIds []int) ([]*model.DropPatternElement, error) {
	var elements []*model.DropPatternElement
	err := r.DB.NewSelect().
		Model(&elements).
		Where("drop_pattern_id IN (?)", bun.In(patternIds)).
		Order("drop_pattern_id", "quantity DESC", "item_id").
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		elements = make([]*model.DropPatternElement, 0)
	} else if err != nil {
		return nil, err
	}
	return elements, nil
}
