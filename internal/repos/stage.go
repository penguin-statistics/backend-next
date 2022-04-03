package repos

import (
	"context"
	"database/sql"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	modelsv2 "github.com/penguin-statistics/backend-next/internal/models/v2"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
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
		Order("stage_id ASC").
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return stages, nil
}

func (c *StageRepo) GetStageById(ctx context.Context, stageId int) (*models.Stage, error) {
	var stage models.Stage
	err := c.db.NewSelect().
		Model(&stage).
		Where("stage_id = ?", stageId).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &stage, nil
}

func (c *StageRepo) GetStageByArkId(ctx context.Context, arkStageId string) (*models.Stage, error) {
	var stage models.Stage
	err := c.db.NewSelect().
		Model(&stage).
		Where("ark_stage_id = ?", arkStageId).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &stage, nil
}

func (c *StageRepo) GetShimStages(ctx context.Context, server string) ([]*modelsv2.Stage, error) {
	var stages []*modelsv2.Stage

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
				Relation("TimeRange", func(sq *bun.SelectQuery) *bun.SelectQuery {
					return sq.Where("start_time <= ? AND end_time > ?", time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
				}).
				Where("drop_info.server = ?", server)
		}).
		Order("stage_id ASC").
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return stages, nil
}

func (c *StageRepo) GetShimStageByArkId(ctx context.Context, arkStageId string, server string) (*modelsv2.Stage, error) {
	var stage modelsv2.Stage
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
				Relation("TimeRange", func(sq *bun.SelectQuery) *bun.SelectQuery {
					return sq.Where("start_time <= ? AND end_time > ?", time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
				}).
				Where("drop_info.server = ?", server)
		}).
		Where("ark_stage_id = ?", arkStageId).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		log.Error().
			Str("stageId", arkStageId).
			Err(err).
			Msg("failed to get shim stage")
		return nil, err
	}

	return &stage, nil
}

func (c *StageRepo) GetStageExtraProcessTypeByArkId(ctx context.Context, arkStageId string) (null.String, error) {
	var stage models.Stage
	err := c.db.NewSelect().
		Model(&stage).
		Column("st.extra_process_type").
		Where("st.ark_stage_id = ?", arkStageId).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return null.NewString("", false), pgerr.ErrNotFound
	} else if err != nil {
		return null.NewString("", false), err
	}

	return stage.ExtraProcessType, nil
}

func (c *StageRepo) SearchStageByCode(ctx context.Context, code string) (*models.Stage, error) {
	var stage models.Stage
	err := c.db.NewSelect().
		Model(&stage).
		Where("\"code\"::TEXT ILIKE ?", "%"+code+"%").
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &stage, nil
}

func (c *StageRepo) GetGachaBoxStages(ctx context.Context) ([]*models.Stage, error) {
	var stages []*models.Stage
	err := c.db.NewSelect().
		Model(&stages).
		Where("extra_process_type = ?", constants.ExtraProcessTypeGachaBox).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return stages, nil
}
