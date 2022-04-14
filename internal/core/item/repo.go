package item

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
)

type Repo struct {
	DB *bun.DB
}

func NewRepo(db *bun.DB) *Repo {
	return &Repo{DB: db}
}

func (c *Repo) GetItems(ctx context.Context) ([]*Model, error) {
	var items []*Model
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

func (c *Repo) GetItemById(ctx context.Context, itemId int) (*Model, error) {
	var item Model
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

func (c *Repo) GetItemByArkId(ctx context.Context, arkItemId string) (*Model, error) {
	var item Model
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

func (c *Repo) GetShimItems(ctx context.Context) ([]*modelv2.Item, error) {
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

func (c *Repo) GetShimItemByArkId(ctx context.Context, itemId string) (*modelv2.Item, error) {
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

func (c *Repo) SearchItemByName(ctx context.Context, name string) (*Model, error) {
	var item Model
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
