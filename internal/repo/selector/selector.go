package selector

import (
	"context"
	"database/sql"
	"errors"

	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/pkg/pgerr"
)

type S[T any] struct {
	DB *bun.DB
}

func New[T any](db *bun.DB) S[T] {
	return S[T]{
		DB: db,
	}
}

func (r S[T]) SelectOne(ctx context.Context, fn func(q *bun.SelectQuery) *bun.SelectQuery) (*T, error) {
	var model T
	err := fn(r.DB.NewSelect().Model(&model)).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &model, nil
}

func (r S[T]) SelectMany(ctx context.Context, fn func(q *bun.SelectQuery) *bun.SelectQuery) ([]*T, error) {
	var model []*T
	err := fn(r.DB.NewSelect().Model(&model)).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return model, nil
}
