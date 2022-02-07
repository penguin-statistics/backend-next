package repos

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
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

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return items, nil
}

func (c *ItemRepo) GetItemById(ctx context.Context, itemId int) (*models.Item, error) {
	var item models.Item
	err := cache.ItemFromId.Get(strconv.Itoa(itemId), &item)
	if err == nil {
		return &item, nil
	}

	err = c.DB.NewSelect().
		Model(&item).
		Where("item_id = ?", itemId).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	go cache.ItemFromId.Set(strconv.Itoa(itemId), &item)
	return &item, nil
}

func (c *ItemRepo) GetItemByArkId(ctx context.Context, arkItemId string) (*models.Item, error) {
	var item models.Item
	err := cache.ItemFromArkId.Get(arkItemId, &item)
	if err == nil {
		return &item, nil
	}

	err = c.DB.NewSelect().
		Model(&item).
		Where("ark_item_id = ?", arkItemId).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	go cache.ItemFromArkId.Set(arkItemId, &item)
	return &item, nil
}

func (c *ItemRepo) GetShimItems(ctx context.Context) ([]*shims.Item, error) {
	var items []*shims.Item

	err := c.DB.NewSelect().
		Model(&items).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
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

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &item, nil
}
