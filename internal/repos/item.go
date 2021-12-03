package repos

import (
	"context"
	"database/sql"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
	"github.com/uptrace/bun"
)

type ItemRepo struct {
	db *bun.DB
}

func NewItemRepo(db *bun.DB) *ItemRepo {
	return &ItemRepo{db: db}
}

func (c *ItemRepo) GetItems(ctx context.Context) ([]*models.PItem, error) {
	var items []*models.PItem
	err := c.db.NewSelect().
		Model(&items).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	}

	if err != nil {
		return nil, err
	}

	return items, nil
}

func (c *ItemRepo) GetItemById(ctx context.Context, itemId string) (*models.PItem, error) {
	var item models.PItem
	err := c.db.NewSelect().
		Model(&item).
		Where("id = ?", itemId).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	}

	if err != nil {
		return nil, err
	}

	return &item, nil
}

func (c *ItemRepo) GetShimItems(ctx context.Context) ([]*shims.PItem, error) {
	var items []*shims.PItem

	err := c.db.NewSelect().
		Model(&items).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	}

	if err != nil {
		return nil, err
	}

	return items, nil
}

func (c *ItemRepo) GetShimItemById(ctx context.Context, itemId string) (*shims.PItem, error) {
	var item shims.PItem
	err := c.db.NewSelect().
		Model(&item).
		Where("ark_item_id = ?", itemId).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	}

	if err != nil {
		return nil, err
	}

	return &item, nil
}
