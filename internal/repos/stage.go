package repos

import (
	"context"
	"database/sql"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
	"github.com/uptrace/bun"
)

type StageRepo struct {
	db *bun.DB
}

func NewStageRepo(db *bun.DB) *StageRepo {
	return &StageRepo{db: db}
}

func (c *StageRepo) GetStages(ctx context.Context) ([]*models.PStage, error) {
	var stages []*models.PStage
	err := c.db.NewSelect().
		Model(&stages).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	}

	if err != nil {
		return nil, err
	}

	return stages, nil
}

func (c *StageRepo) GetStageById(ctx context.Context, itemId string) (*models.PStage, error) {
	var stage models.PStage
	err := c.db.NewSelect().
		Model(&stage).
		Where("id = ?", itemId).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	}

	if err != nil {
		return nil, err
	}

	return &stage, nil
}

// func (c *StageRepo) GetShimItems(ctx context.Context) ([]*shims.PStage, error) {
// 	var items []*shims.PItem

// 	err := c.db.NewSelect().
// 		Model(&items).
// 		Scan(ctx)

// 	if err == sql.ErrNoRows {
// 		return nil, errors.ErrNotFound
// 	}

// 	if err != nil {
// 		return nil, err
// 	}

// 	return items, nil
// }

// func (c *StageRepo) GetShimItemById(ctx context.Context, itemId string) (*shims.PItem, error) {
// 	var item shims.PItem
// 	err := c.db.NewSelect().
// 		Model(&item).
// 		Where("ark_item_id = ?", itemId).
// 		Scan(ctx)

// 	if err == sql.ErrNoRows {
// 		return nil, errors.ErrNotFound
// 	}

// 	if err != nil {
// 		return nil, err
// 	}

// 	return &item, nil
// }
