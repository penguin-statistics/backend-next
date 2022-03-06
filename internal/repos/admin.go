package repos

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
)

type AdminRepo struct {
	db *bun.DB
}

func NewAdminRepo(db *bun.DB) *AdminRepo {
	return &AdminRepo{db: db}
}

func (r *AdminRepo) SaveZones(ctx context.Context, tx bun.Tx, zones *[]*models.Zone) error {
	_, err := tx.NewInsert().
		On("CONFLICT (ark_zone_id) DO UPDATE").
		Model(zones).
		Exec(ctx)
	return err
}

func (r *AdminRepo) SaveActivities(ctx context.Context, tx bun.Tx, activities *[]*models.Activity) error {
	_, err := tx.NewInsert().
		On("CONFLICT (activity_id) DO UPDATE").
		Model(activities).
		Exec(ctx)
	return err
}

func (r *AdminRepo) SaveTimeRanges(ctx context.Context, tx bun.Tx, timeRanges *[]*models.TimeRange) error {
	_, err := tx.NewInsert().
		On("CONFLICT (range_id) DO UPDATE").
		Model(timeRanges).
		Exec(ctx)
	return err
}

func (r *AdminRepo) SaveStages(ctx context.Context, tx bun.Tx, stages *[]*models.Stage) error {
	_, err := tx.NewInsert().
		On("CONFLICT (ark_stage_id) DO UPDATE").
		Model(stages).
		Exec(ctx)
	return err
}

func (r *AdminRepo) SaveDropInfos(ctx context.Context, tx bun.Tx, dropInfos *[]*models.DropInfo) error {
	_, err := tx.NewInsert().
		On("CONFLICT (drop_id) DO UPDATE").
		Model(dropInfos).
		Exec(ctx)
	return err
}
