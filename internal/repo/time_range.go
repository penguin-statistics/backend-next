package repo

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
)

type TimeRange struct {
	db *bun.DB
}

func NewTimeRange(db *bun.DB) *TimeRange {
	return &TimeRange{db: db}
}

func (c *TimeRange) GetTimeRangesByServer(ctx context.Context, server string) ([]*models.TimeRange, error) {
	var timeRanges []*models.TimeRange
	if err := c.db.NewSelect().
		Model(&timeRanges).
		Where("tr.server = ?", server).
		Scan(ctx); err != nil {
		return nil, err
	}
	return timeRanges, nil
}

func (c *TimeRange) GetTimeRangeById(ctx context.Context, rangeId int) (*models.TimeRange, error) {
	var timeRange models.TimeRange
	if err := c.db.NewSelect().
		Model(&timeRange).
		Where("tr.range_id = ?", rangeId).
		Scan(ctx); err != nil {
		return nil, err
	}

	return &timeRange, nil
}
