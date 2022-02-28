package repos

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
)

type ItemRepo struct {
	DB *bun.DB
}

func NewItemRepo(db *bun.DB) *ItemRepo {
	return &ItemRepo{DB: db}
}

func (c *ItemRepo) GetItems(ctx context.Context) ([]*models.Item, error) {
	var items []*models.Item
	err := c.DB.NewSelect().
		Model(&items).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return items, nil
}

func (c *ItemRepo) GetItemById(ctx context.Context, itemId int) (*models.Item, error) {
	var item models.Item
	err := c.DB.NewSelect().
		Model(&item).
		Where("item_id = ?", itemId).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &item, nil
}

func (c *ItemRepo) GetItemByArkId(ctx context.Context, arkItemId string) (*models.Item, error) {
	var item models.Item
	err := c.DB.NewSelect().
		Model(&item).
		Where("ark_item_id = ?", arkItemId).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &item, nil
}

func (c *ItemRepo) GetShimItems(ctx context.Context) ([]*shims.Item, error) {
	var items []*shims.Item

	err := c.DB.NewSelect().
		Model(&items).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return items, nil
}

func (c *ItemRepo) GetShimItemByArkId(ctx context.Context, itemId string) (*shims.Item, error) {
	var item shims.Item
	err := c.DB.NewSelect().
		Model(&item).
		Where("ark_item_id = ?", itemId).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &item, nil
}

func (c *ItemRepo) SearchItemByName(ctx context.Context, name string) (*models.Item, error) {
	var item models.Item
	err := c.DB.NewSelect().
		Model(&item).
		Where("\"name\"::TEXT ILIKE ?", "%"+name+"%").
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &item, nil
}
