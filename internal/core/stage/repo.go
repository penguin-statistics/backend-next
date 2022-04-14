package stage

import (
	"context"
	"database/sql"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constant"
	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
)

type Repo struct {
	db *bun.DB
}

func NewRepo(db *bun.DB) *Repo {
	return &Repo{db: db}
}

func (c *Repo) GetStages(ctx context.Context) ([]*Model, error) {
	var stages []*Model
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

func (c *Repo) GetStageById(ctx context.Context, stageId int) (*Model, error) {
	var stage Model
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

func (c *Repo) GetStageByArkId(ctx context.Context, arkStageId string) (*Model, error) {
	var stage Model
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

func (c *Repo) GetShimStages(ctx context.Context, server string) ([]*modelv2.Stage, error) {
	var stages []*modelv2.Stage

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

func (c *Repo) GetShimStageByArkId(ctx context.Context, arkStageId string, server string) (*modelv2.Stage, error) {
	var stage modelv2.Stage
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

func (c *Repo) GetStageExtraProcessTypeByArkId(ctx context.Context, arkStageId string) (null.String, error) {
	var stage Model
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

func (c *Repo) SearchStageByCode(ctx context.Context, code string) (*Model, error) {
	var stage Model
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

func (c *Repo) GetGachaBoxStages(ctx context.Context) ([]*Model, error) {
	var stages []*Model
	err := c.db.NewSelect().
		Model(&stages).
		Where("extra_process_type = ?", constant.ExtraProcessTypeGachaBox).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return stages, nil
}
