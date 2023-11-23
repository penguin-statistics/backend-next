package repo

import (
	"context"

	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo/selector"
)

type TimeRange struct {
	db  *bun.DB
	sel selector.S[model.TimeRange]
}

func NewTimeRange(db *bun.DB) *TimeRange {
	return &TimeRange{db: db, sel: selector.New[model.TimeRange](db)}
}

func (r *TimeRange) GetTimeRangesByServer(ctx context.Context, server string) ([]*model.TimeRange, error) {
	var timeRanges []*model.TimeRange
	if err := r.db.NewSelect().
		Model(&timeRanges).
		Where("tr.server = ?", server).
		Scan(ctx); err != nil {
		return nil, err
	}
	return timeRanges, nil
}

func (r *TimeRange) GetTimeRangeById(ctx context.Context, rangeId int) (*model.TimeRange, error) {
	var timeRange model.TimeRange
	if err := r.db.NewSelect().
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
