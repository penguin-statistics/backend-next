package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
)

type DropPatternElement struct {
	DB *bun.DB
}

func NewDropPatternElement(db *bun.DB) *DropPatternElement {
	return &DropPatternElement{DB: db}
}

func (r *DropPatternElement) GetDropPatternElementById(ctx context.Context, id int) (*models.DropPatternElement, error) {
	var DropPatternElement models.DropPatternElement
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

func (r *DropPatternElement) GetDropPatternElementByHash(ctx context.Context, hash string) (*models.DropPatternElement, error) {
	var DropPatternElement models.DropPatternElement
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

func (r *DropPatternElement) CreateDropPatternElements(ctx context.Context, tx bun.Tx, patternId int, drops []*types.Drop) ([]*models.DropPatternElement, error) {
	elements := make([]models.DropPatternElement, 0, len(drops))
	for _, drop := range drops {
		element := models.DropPatternElement{
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

	ptrElements := make([]*models.DropPatternElement, 0, len(elements))
	for i := range elements {
		ptrElements = append(ptrElements, &elements[i])
	}

	return ptrElements, nil
}

func (r *DropPatternElement) GetDropPatternElementsByPatternId(ctx context.Context, patternId int) ([]*models.DropPatternElement, error) {
	var elements []*models.DropPatternElement
	err := r.DB.NewSelect().
		Model(&elements).
		Where("drop_pattern_id = ?", patternId).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		elements = make([]*models.DropPatternElement, 0)
	} else if err != nil {
		return nil, err
	}
	return elements, nil
}
