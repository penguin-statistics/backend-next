package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/repo/selector"
)

type Zone struct {
	db    *bun.DB
	v2sel selector.S[modelv2.Zone]
	v3sel selector.S[model.Zone]
}

func NewZone(db *bun.DB) *Zone {
	return &Zone{
		db:    db,
		v2sel: selector.New[modelv2.Zone](db),
		v3sel: selector.New[model.Zone](db),
	}
}

func (c *Zone) GetZones(ctx context.Context) ([]*model.Zone, error) {
	return c.v3sel.SelectMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Order("zone_id ASC")
	})
}

func (c *Zone) GetZoneById(ctx context.Context, id int) (*model.Zone, error) {
	return c.v3sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("zone_id = ?", id)
	})
}

func (c *Zone) GetZoneByArkId(ctx context.Context, arkZoneId string) (*model.Zone, error) {
	return c.v3sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("ark_zone_id = ?", arkZoneId)
	})
}

func (c *Zone) GetShimZones(ctx context.Context) ([]*modelv2.Zone, error) {
	var zones []*modelv2.Zone

	err := c.db.NewSelect().
		Model(&zones).
		// `Stages` shall match the model's `Stages` field name, rather for any else references
		Relation("Stages", func(q *bun.SelectQuery) *bun.SelectQuery {
			// zone_id is for go-pg/pg's internal joining for has-many relationship. removing it will cause an error
			// see https://github.com/go-pg/pg/issues/1315
			return q.Column("ark_stage_id", "zone_id")
		}).
		Order("zone_id ASC").
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return zones, nil
}

func (c *Zone) GetShimZoneByArkId(ctx context.Context, arkZoneId string) (*modelv2.Zone, error) {
	var zone modelv2.Zone
	err := c.db.NewSelect().
		Model(&zone).
		Relation("Stages", func(q *bun.SelectQuery) *bun.SelectQuery {
			// zone_id is for go-pg/pg's internal joining for has-many relationship. removing it will cause an error
			// see https://github.com/go-pg/pg/issues/1315
			return q.Column("ark_stage_id", "zone_id")
		}).
		Where("ark_zone_id = ?", arkZoneId).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &zone, nil
}
