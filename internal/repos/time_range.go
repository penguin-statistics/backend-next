package repos

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
)

type TimeRangeRepo struct {
	db *bun.DB
}

func NewTimeRangeRepo(db *bun.DB) *TimeRangeRepo {
	return &TimeRangeRepo{db: db}
}

func (c *TimeRangeRepo) GetTimeRangesByServer(ctx context.Context, server string) ([]*models.TimeRange, error) {
	var timeRanges []*models.TimeRange
	if err := c.db.NewSelect().
		Model(&timeRanges).
		Where("tr.server = ?", server).
		Scan(ctx); err != nil {
		return nil, err
	}
	return timeRanges, nil
}
