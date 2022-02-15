package repos

import (
	"context"
	"database/sql"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
)

type ActivityRepo struct {
	DB *bun.DB
}

func NewActivityRepo(db *bun.DB) *ActivityRepo {
	return &ActivityRepo{DB: db}
}

func (c *ActivityRepo) GetActivities(ctx context.Context) ([]*models.Activity, error) {
	var activities []*models.Activity
	err := c.DB.NewSelect().
		Model(&activities).
		Scan(ctx)

	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	return activities, nil
}
