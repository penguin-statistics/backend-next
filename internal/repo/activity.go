package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
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

func (r *Activity) GetActivities(ctx context.Context) ([]*model.Activity, error) {
	return r.sel.SelectMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Order("activity_id ASC")
	})
}

func (r *Activity) GetActivityById(ctx context.Context, activityId int) (*model.Activity, error) {
	var activity model.Activity
	err := r.db.NewSelect().
		Model(&activity).
		Where("activity_id = ?", activityId).
		Scan(ctx)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	return &activity, nil
}
