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
	db    *bun.DB
	v2sel selector.S[modelv2.Stage]
	v3sel selector.S[model.Stage]
}

func NewStage(db *bun.DB) *Stage {
	return &Stage{
		db:    db,
		v2sel: selector.New[modelv2.Stage](db),
		v3sel: selector.New[model.Stage](db),
	}
}

func (r *Stage) GetStages(ctx context.Context) ([]*model.Stage, error) {
	return r.v3sel.SelectMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Order("stage_id ASC")
	})
}

func (r *Stage) GetStageById(ctx context.Context, stageId int) (*model.Stage, error) {
	return r.v3sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("stage_id = ?", stageId)
	})
}

func (r *Stage) GetStageByArkId(ctx context.Context, arkStageId string) (*model.Stage, error) {
	return r.v3sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("ark_stage_id = ?", arkStageId)
	})
}

func (r *Stage) shimStageQuery(ctx context.Context, server string, stages *[]*modelv2.Stage, t time.Time) error {
	return r.db.NewSelect().
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

func (r *Stage) GetShimStages(ctx context.Context, server string) ([]*modelv2.Stage, error) {
	var stages []*modelv2.Stage

	err := r.shimStageQuery(ctx, server, &stages, time.Now())

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return stages, nil
}

func (r *Stage) GetShimStagesForFakeTime(ctx context.Context, server string, fakeTime time.Time) ([]*modelv2.Stage, error) {
	var stages []*modelv2.Stage

	err := r.shimStageQuery(ctx, server, &stages, fakeTime)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return stages, nil
}

func (r *Stage) GetShimStageByArkId(ctx context.Context, arkStageId string, server string) (*modelv2.Stage, error) {
	var stage modelv2.Stage
	err := r.db.NewSelect().
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

func (r *Stage) GetStageExtraProcessTypeByArkId(ctx context.Context, arkStageId string) (null.String, error) {
	stage, err := r.v3sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Column("st.extra_process_type").Where("st.ark_stage_id = ?", arkStageId)
	})
	if err != nil {
		return null.NewString("", false), err
	}

	return stage.ExtraProcessType, nil
}

func (r *Stage) SearchStageByCode(ctx context.Context, code string) (*model.Stage, error) {
	return r.v3sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("\"code\"::TEXT ILIKE ?", "%"+code+"%")
	})
}

func (r *Stage) GetGachaBoxStages(ctx context.Context) ([]*model.Stage, error) {
	return r.v3sel.SelectMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("st.extra_process_type = ?", constant.ExtraProcessTypeGachaBox)
	})
}
