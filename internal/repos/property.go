package repos

import (
	"context"
	"database/sql"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
)

type PropertyRepo struct {
	db *bun.DB
}

func NewPropertyRepo(db *bun.DB) *PropertyRepo {
	return &PropertyRepo{db: db}
}

func (c *PropertyRepo) GetProperties(ctx context.Context) ([]*models.Property, error) {
	var properties []*models.Property
	err := c.db.NewSelect().
		Model(&properties).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return properties, nil
}

func (c *PropertyRepo) GetPropertyByKey(ctx context.Context, key string) (*models.Property, error) {
	var property models.Property
	err := c.db.NewSelect().
		Model(&property).
		Where("key = ?", key).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &property, nil
}