package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
	modelv2 "github.com/penguin-statistics/backend-next/internal/models/v2"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
)

type Item struct {
	DB *bun.DB
}

func NewItem(db *bun.DB) *Item {
	return &Item{DB: db}
}

func (c *Item) GetItems(ctx context.Context) ([]*models.Item, error) {
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

func (c *Item) GetItemById(ctx context.Context, itemId int) (*models.Item, error) {
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

func (c *Item) GetItemByArkId(ctx context.Context, arkItemId string) (*models.Item, error) {
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

func (c *Item) GetShimItems(ctx context.Context) ([]*modelv2.Item, error) {
	var items []*modelv2.Item

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

func (c *Item) GetShimItemByArkId(ctx context.Context, itemId string) (*modelv2.Item, error) {
	var item modelv2.Item
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

func (c *Item) SearchItemByName(ctx context.Context, name string) (*models.Item, error) {
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
