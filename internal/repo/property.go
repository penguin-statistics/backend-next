package repo

import (
	"context"

	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo/selector"
)

type Property struct {
	db  *bun.DB
	sel selector.S[model.Property]
}

func NewProperty(db *bun.DB) *Property {
	return &Property{db: db, sel: selector.New[model.Property](db)}
}

func (r *Property) GetProperties(ctx context.Context) ([]*model.Property, error) {
	return r.sel.SelectMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Order("property_id ASC")
	})
}

func (r *Property) GetPropertyByKey(ctx context.Context, key string) (*model.Property, error) {
	return r.sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("key = ?", key)
	})
}

func (c *Property) UpdatePropertyByKey(ctx context.Context, key string, value string) (*model.Property, error) {
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

	property.Value = value
	_, err = c.db.NewUpdate().
		Model(&property).
		Where("key = ?", key).
		Exec(ctx)

	if err != nil {
		return nil, err
	}

	return &property, nil
}
