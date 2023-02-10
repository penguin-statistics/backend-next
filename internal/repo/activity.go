package repo

import (
	"context"

	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo/selector"
)

type Activity struct {
	db  *bun.DB
	sel selector.S[model.Activity]
}

func NewActivity(db *bun.DB) *Activity {
	return &Activity{db: db, sel: selector.New[model.Activity](db)}
}

func (c *Activity) GetActivities(ctx context.Context) ([]*model.Activity, error) {
	return c.sel.SelectMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Order("activity_id ASC")
	})
}
