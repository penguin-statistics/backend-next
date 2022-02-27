package repos

import (
	"context"
	"database/sql"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
)

type ZoneRepo struct {
	db *bun.DB
}

func NewZoneRepo(db *bun.DB) *ZoneRepo {
	return &ZoneRepo{db: db}
}

func (c *ZoneRepo) SaveZones(ctx context.Context, tx bun.Tx, zones *[]*models.Zone) error {
	_, err := tx.NewInsert().Model(zones).Exec(ctx)
	return err
}

// Cache: AllZones
func (c *ZoneRepo) GetZones(ctx context.Context) ([]*models.Zone, error) {
	var zones []*models.Zone
	err := c.db.NewSelect().
		Model(&zones).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return zones, nil
}

func (c *ZoneRepo) GetZoneById(ctx context.Context, id int) (*models.Zone, error) {
	var zone models.Zone
	err := c.db.NewSelect().
		Model(&zone).
		Where("zone_id = ?", id).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &zone, nil
}

// Cache: ZoneFromArkId
func (c *ZoneRepo) GetZoneByArkId(ctx context.Context, arkZoneId string) (*models.Zone, error) {
	var zone models.Zone
	err := c.db.NewSelect().
		Model(&zone).
		Where("ark_zone_id = ?", arkZoneId).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &zone, nil
}

func (c *ZoneRepo) GetShimZones(ctx context.Context) ([]*shims.Zone, error) {
	var zones []*shims.Zone

	err := c.db.NewSelect().
		Model(&zones).
		// `Stages` shall match the model's `Stages` field name, rather for any else references
		Relation("Stages", func(q *bun.SelectQuery) *bun.SelectQuery {
			// zone_id is for go-pg/pg's internal joining for has-many relationship. removing it will cause an error
			// see https://github.com/go-pg/pg/issues/1315
			return q.Column("ark_stage_id", "zone_id")
		}).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return zones, nil
}

func (c *ZoneRepo) GetShimZoneByArkId(ctx context.Context, arkZoneId string) (*shims.Zone, error) {
	var zone shims.Zone
	err := c.db.NewSelect().
		Model(&zone).
		Relation("Stages", func(q *bun.SelectQuery) *bun.SelectQuery {
			// zone_id is for go-pg/pg's internal joining for has-many relationship. removing it will cause an error
			// see https://github.com/go-pg/pg/issues/1315
			return q.Column("ark_stage_id", "zone_id")
		}).
		Where("ark_zone_id = ?", arkZoneId).
		Scan(ctx)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &zone, nil
}
