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

func (c *TimeRangeRepo) SaveTimeRanges(ctx context.Context, tx bun.Tx, timeRanges *[]*models.TimeRange) error {
	_, err := tx.NewInsert().
		Model(timeRanges).
		Exec(ctx)
	return err
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

func (c *TimeRangeRepo) GetTimeRangeById(ctx context.Context, rangeId int) (*models.TimeRange, error) {
	var timeRange models.TimeRange
	if err := c.db.NewSelect().
		Model(&timeRange).
		Where("tr.range_id = ?", rangeId).
		Scan(ctx); err != nil {
		return nil, err
	}

	return &timeRange, nil
}
