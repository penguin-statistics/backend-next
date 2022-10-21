package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
)

type Property struct {
	db *bun.DB
}

func NewProperty(db *bun.DB) *Property {
	return &Property{db: db}
}

func (c *Property) GetProperties(ctx context.Context) ([]*model.Property, error) {
	var properties []*model.Property
	err := c.db.NewSelect().
		Model(&properties).
		Order("property_id ASC").
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return properties, nil
}

func (c *Property) GetPropertyByKey(ctx context.Context, key string) (*model.Property, error) {
	var property model.Property
	err := c.db.NewSelect().
		Model(&property).
		Where("key = ?", key).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &property, nil
}
