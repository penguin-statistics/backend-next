package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
)

type Activity struct {
	DB *bun.DB
}

func NewActivity(db *bun.DB) *Activity {
	return &Activity{DB: db}
}

func (c *Activity) GetActivities(ctx context.Context) ([]*model.Activity, error) {
	var activities []*model.Activity
	err := c.DB.NewSelect().
		Model(&activities).
		Order("activity_id ASC").
		Scan(ctx)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	return activities, nil
}

func (c *Activity) GetActivityById(ctx context.Context, activityId int) (*model.Activity, error) {
	var activity model.Activity
	err := c.DB.NewSelect().
		Model(&activity).
		Where("activity_id = ?", activityId).
		Scan(ctx)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	return &activity, nil
}
