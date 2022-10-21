package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
)

type Zone struct {
	db *bun.DB
}

func NewZone(db *bun.DB) *Zone {
	return &Zone{db: db}
}

func (c *Zone) GetZones(ctx context.Context) ([]*model.Zone, error) {
	var zones []*model.Zone
	err := c.db.NewSelect().
		Model(&zones).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return zones, nil
}

func (c *Zone) GetZoneById(ctx context.Context, id int) (*model.Zone, error) {
	var zone model.Zone
	err := c.db.NewSelect().
		Model(&zone).
		Where("zone_id = ?", id).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &zone, nil
}

func (c *Zone) GetZoneByArkId(ctx context.Context, arkZoneId string) (*model.Zone, error) {
	var zone model.Zone
	err := c.db.NewSelect().
		Model(&zone).
		Where("ark_zone_id = ?", arkZoneId).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &zone, nil
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
