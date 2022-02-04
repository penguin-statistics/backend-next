package repos

import (
	"context"
	"database/sql"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
)

type DropPatternElementRepo struct {
	DB *bun.DB
}

func NewDropPatternElementRepo(db *bun.DB) *DropPatternElementRepo {
	return &DropPatternElementRepo{DB: db}
}

func (r *DropPatternElementRepo) GetDropPatternElementById(ctx context.Context, id int) (*models.DropPatternElement, error) {
	var DropPatternElement models.DropPatternElement
	err := r.DB.NewSelect().
		Model(&DropPatternElement).
		Where("id = ?", id).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &DropPatternElement, nil
}

func (r *DropPatternElementRepo) GetDropPatternElementByHash(ctx context.Context, hash string) (*models.DropPatternElement, error) {
	var DropPatternElement models.DropPatternElement
	err := r.DB.NewSelect().
		Model(&DropPatternElement).
		Where("hash = ?", hash).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &DropPatternElement, nil
}

func (r *DropPatternElementRepo) CreateDropPatternElements(ctx context.Context, tx bun.Tx, patternId int, drops []*types.Drop) ([]*models.DropPatternElement, error) {
	var elements []*models.DropPatternElement
	for _, drop := range drops {
		element := &models.DropPatternElement{
			ItemID:        drop.ItemID,
			Quantity:      drop.Quantity,
			DropPatternID: patternId,
		}
		elements = append(elements, element)
	}

	_, err := r.DB.NewInsert().
		Model(&elements).
		Exec(ctx)
	if err != nil {
		return nil, err
	}

	return elements, nil
}

func (r *DropPatternElementRepo) GetDropPatternElementsByPatternIds(ctx context.Context, patternIds []int) ([]*models.DropPatternElement, error) {
	var elements []*models.DropPatternElement
	err := r.DB.NewSelect().
		Model(&elements).
		Apply(func(q *bun.SelectQuery) *bun.SelectQuery {
			if len(patternIds) > 0 {
				return q.Where("drop_pattern_id in (?)", bun.In(patternIds))
			}
			return q
		}).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return elements, nil
}
