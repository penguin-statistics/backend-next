package repo

import (
	"context"

	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
)

type TimeRange struct {
	db *bun.DB
}

func NewTimeRange(db *bun.DB) *TimeRange {
	return &TimeRange{db: db}
}

func (c *TimeRange) GetTimeRangesByServer(ctx context.Context, server string) ([]*model.TimeRange, error) {
	var timeRanges []*model.TimeRange
	if err := c.db.NewSelect().
		Model(&timeRanges).
		Where("tr.server = ?", server).
		Scan(ctx); err != nil {
		return nil, err
	}
	return timeRanges, nil
}

func (c *TimeRange) GetTimeRangeById(ctx context.Context, rangeId int) (*model.TimeRange, error) {
	var timeRange model.TimeRange
	if err := c.db.NewSelect().
		Model(&timeRange).
		Where("tr.range_id = ?", rangeId).
		Scan(ctx); err != nil {
		return nil, err
	}

	return &timeRange, nil
}

func (c *TimeRange) GetTimeRangeByServerAndName(ctx context.Context, server string, name string) (*model.TimeRange, error) {
	var timeRange model.TimeRange
	if err := c.db.NewSelect().
		Model(&timeRange).
		Where("tr.server = ?", server).
		Where("tr.name = ?", name).
		Scan(ctx); err != nil {
		return nil, err
	}

	return &timeRange, nil
}
