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
