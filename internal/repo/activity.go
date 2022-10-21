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
		Scan(ctx)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	return activities, nil
}
