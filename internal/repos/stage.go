package repos

import (
	"context"
	"database/sql"

	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
	"github.com/uptrace/bun"
)

type StageRepo struct {
	db *bun.DB
}

func NewStageRepo(db *bun.DB) *StageRepo {
	return &StageRepo{db: db}
}

func (c *StageRepo) GetStages(ctx context.Context) ([]*models.Stage, error) {
	var stages []*models.Stage
	err := c.db.NewSelect().
		Model(&stages).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return stages, nil
}

func (c *StageRepo) GetStageByArkId(ctx context.Context, stageArkId string) (*models.Stage, error) {
	val, ok := cache.StageFromArkId.Get(stageArkId)
	if ok {
		return val.(*models.Stage), nil
	}

	var stage models.Stage
	err := c.db.NewSelect().
		Model(&stage).
		Where("ark_stage_id = ?", stageArkId).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	cache.StageFromArkId.SetDefault(stageArkId, &stage)
	return &stage, nil
}

func (c *StageRepo) GetShimStages(ctx context.Context, server string) ([]*shims.Stage, error) {
	var stages []*shims.Stage

	err := c.db.NewSelect().
		Model(&stages).
		Relation("Zone", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Column("ark_zone_id")
		}).
		Relation("DropInfos", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.
				Relation("Item", func(sq *bun.SelectQuery) *bun.SelectQuery {
					return sq.Column("ark_item_id")
				}).
				Relation("Stage", func(sq *bun.SelectQuery) *bun.SelectQuery {
					return sq.Column("ark_stage_id")
				}).
				Where("server = ?", server)
		}).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return stages, nil
}

func (c *StageRepo) GetShimStageByArkId(ctx context.Context, stageId string, server string) (*shims.Stage, error) {
	var stage shims.Stage
	err := c.db.NewSelect().
		Model(&stage).
		Relation("Zone", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Column("ark_zone_id")
		}).
		Relation("DropInfos", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.
				Relation("Item", func(sq *bun.SelectQuery) *bun.SelectQuery {
					return sq.Column("ark_item_id")
				}).
				Relation("Stage", func(sq *bun.SelectQuery) *bun.SelectQuery {
					return sq.Column("ark_stage_id")
				}).
				Where("server = ?", server)
		}).
		Where("ark_stage_id = ?", stageId).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		log.Error().
			Str("stageId", stageId).
			Err(err).
			Msg("failed to get shim stage")
		return nil, err
	}

	return &stage, nil
}
