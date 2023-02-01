package repo

import (
	"context"
	"database/sql"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/repo/selector"
)

type Stage struct {
	sel selector.S[model.Stage]

	db *bun.DB
}

func NewStage(db *bun.DB) *Stage {
	return &Stage{db: db, sel: selector.New[model.Stage](db)}
}

func (c *Stage) GetStages(ctx context.Context) ([]*model.Stage, error) {
	return c.sel.SelectMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Order("stage_id ASC")
	})
}

func (c *Stage) GetStageById(ctx context.Context, stageId int) (*model.Stage, error) {
	return c.sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("stage_id = ?", stageId)
	})
}

func (c *Stage) GetStageByArkId(ctx context.Context, arkStageId string) (*model.Stage, error) {
	return c.sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("ark_stage_id = ?", arkStageId)
	})
}

func (c *Stage) shimStageQuery(ctx context.Context, server string, stages *[]*modelv2.Stage, t time.Time) error {
	return c.db.NewSelect().
		Model(stages).
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
					return sq.Where("start_time <= ? AND end_time > ?", t.Format(time.RFC3339), t.Format(time.RFC3339))
				}).
				Where("drop_info.server = ?", server)
		}).
		Order("stage_id ASC").
		Scan(ctx)
}

func (c *Stage) GetShimStages(ctx context.Context, server string) ([]*modelv2.Stage, error) {
	var stages []*modelv2.Stage

	err := c.shimStageQuery(ctx, server, &stages, time.Now())

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return stages, nil
}

func (c *Stage) GetShimStagesForFakeTime(ctx context.Context, server string, fakeTime time.Time) ([]*modelv2.Stage, error) {
	var stages []*modelv2.Stage

	err := c.shimStageQuery(ctx, server, &stages, fakeTime)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return stages, nil
}

func (c *Stage) GetShimStageByArkId(ctx context.Context, arkStageId string, server string) (*modelv2.Stage, error) {
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

func (c *Stage) GetStageExtraProcessTypeByArkId(ctx context.Context, arkStageId string) (null.String, error) {
	stage, err := c.sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Column("st.extra_process_type").
			Where("ark_stage_id = ?", arkStageId)
	})
	if err != nil {
		return null.NewString("", false), err
	}

	return stage.ExtraProcessType, nil
}

func (c *Stage) SearchStageByCode(ctx context.Context, code string) (*model.Stage, error) {
	return c.sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("\"code\"::TEXT ILIKE ?", "%"+code+"%")
	})
}

func (c *Stage) GetGachaBoxStages(ctx context.Context) ([]*model.Stage, error) {
	return c.sel.SelectMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("extra_process_type = ?", constant.ExtraProcessTypeGachaBox)
	})
}
