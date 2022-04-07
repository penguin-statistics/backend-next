package admin

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/core/activity"
	"github.com/penguin-statistics/backend-next/internal/model"
)

type Repo struct {
	db *bun.DB
}

func NewRepo(db *bun.DB) *Repo {
	return &Repo{db: db}
}

func (r *Repo) SaveZones(ctx context.Context, tx bun.Tx, zones *[]*model.Zone) error {
	_, err := tx.NewInsert().
		On("CONFLICT (ark_zone_id) DO UPDATE").
		Model(zones).
		Exec(ctx)
	return err
}

func (r *Repo) SaveActivities(ctx context.Context, tx bun.Tx, activities *[]*activity.Model) error {
	_, err := tx.NewInsert().
		On("CONFLICT (activity_id) DO UPDATE").
		Model(activities).
		Exec(ctx)
	return err
}

func (r *Repo) SaveTimeRanges(ctx context.Context, tx bun.Tx, timeRanges *[]*model.TimeRange) error {
	_, err := tx.NewInsert().
		On("CONFLICT (range_id) DO UPDATE").
		Model(timeRanges).
		Exec(ctx)
	return err
}

func (r *Repo) SaveStages(ctx context.Context, tx bun.Tx, stages *[]*model.Stage) error {
	_, err := tx.NewInsert().
		On("CONFLICT (ark_stage_id) DO UPDATE").
		Model(stages).
		Exec(ctx)
	return err
}

func (r *Repo) SaveDropInfos(ctx context.Context, tx bun.Tx, dropInfos *[]*model.DropInfo) error {
	_, err := tx.NewInsert().
		On("CONFLICT (drop_id) DO UPDATE").
		Model(dropInfos).
		Exec(ctx)
	return err
}
