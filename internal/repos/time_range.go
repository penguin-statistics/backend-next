package repos

import (
	"context"
	"strconv"
	"time"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
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

func (c *TimeRangeRepo) GetTimeRangeById(ctx context.Context, rangeId int) (*models.TimeRange, error) {
	var timeRange models.TimeRange
	err := cache.StageFromId.Get(strconv.Itoa(rangeId), &timeRange)
	if err == nil {
		return &timeRange, nil
	}

	if err := c.db.NewSelect().
		Model(&timeRange).
		Where("tr.range_id = ?", rangeId).
		Scan(ctx); err != nil {
		return nil, err
	}

	go cache.StageFromId.Set(strconv.Itoa(rangeId), &timeRange, time.Hour*24)
	return &timeRange, nil
}
