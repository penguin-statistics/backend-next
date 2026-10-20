package repo

import (
	"context"
	"database/sql"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/pkg/errors"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo/selector"
)

type DropMatrixElement struct {
	db  *bun.DB
	sel selector.S[model.DropMatrixElement]
}

func NewDropMatrixElement(db *bun.DB) *DropMatrixElement {
	return &DropMatrixElement{db: db, sel: selector.New[model.DropMatrixElement](db)}
}

func (s *DropMatrixElement) BatchSaveElements(ctx context.Context, elements []*model.DropMatrixElement, server string) error {
	_, err := s.db.NewInsert().Model(&elements).Exec(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (s *DropMatrixElement) DeleteByServerAndDayNum(ctx context.Context, server string, dayNum int) error {
	_, err := s.db.NewDelete().Model((*model.DropMatrixElement)(nil)).Where("server = ?", server).Where("day_num = ?", dayNum).Exec(ctx)
	return err
}

/**
 * startDayNum inclusive
 * endDayNum inclusive
 */
func (s *DropMatrixElement) GetElementsByServerAndSourceCategoryAndDayNumRange(
	ctx context.Context, server string, sourceCategory string, startDayNum int, endDayNum int,
) ([]*model.DropMatrixElement, error) {
	var elements []*model.DropMatrixElement
	err := s.db.NewSelect().Model(&elements).
		Where("server = ?", server).
		Where("source_category = ?", sourceCategory).
		Where("day_num >= ?", startDayNum).
		Where("day_num <= ?", endDayNum).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return elements, nil
}

func (s *DropMatrixElement) IsExistByServerAndDayNum(ctx context.Context, server string, dayNum int) (bool, error) {
	exists, err := s.db.NewSelect().Model((*model.DropMatrixElement)(nil)).Where("server = ?", server).Where("day_num = ?", dayNum).Exists(ctx)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// CalcGlobalDropMatrixAggregates aggregates the elements of every target of server for the source categories in a
// single scan, see model.GlobalDropMatrixAggregate. A single statement also reads a single snapshot, unaffected by a
// concurrent rewrite (delete, then insert) of the elements.
func (s *DropMatrixElement) CalcGlobalDropMatrixAggregates(
	ctx context.Context, server string, targets []*model.GlobalDropMatrixTarget, sourceCategories []string,
) (map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate, error) {
	aggregates := make(map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate)
	if len(targets) == 0 || len(sourceCategories) == 0 {
		return aggregates, nil
	}
	stageIds := make([]int, len(targets))
	itemIds := make([]int, len(targets))
	// compared at second precision, as the time ranges were formatted as RFC 3339 before
	starts := make([]time.Time, len(targets))
	ends := make([]time.Time, len(targets))
	for i, target := range targets {
		stageIds[i], itemIds[i] = target.StageID, target.ItemID
		starts[i] = time.Unix(target.TimeRange.StartTime.Unix(), 0)
		ends[i] = time.Unix(target.TimeRange.EndTime.Unix(), 0)
	}

	var rows []struct {
		Kind           string `bun:"kind"`
		SourceCategory string `bun:"source_category"`
		StageID        int    `bun:"stage_id"`
		ItemID         int    `bun:"item_id"`
		Key            int    `bun:"key"`
		Value          int64  `bun:"value"`
	}
	err := s.db.NewRaw(`WITH t AS (
			SELECT * FROM unnest(?::int[], ?::int[], ?::timestamptz[], ?::timestamptz[]) AS t(stage_id, item_id, start_time, end_time)
		), e AS MATERIALIZED (
			SELECT d.source_category, d.stage_id, d.item_id, d.times, d.day_num, d.quantity, d.quantity_buckets
			FROM drop_matrix_elements AS d
			JOIN t ON d.stage_id = t.stage_id AND d.item_id = t.item_id AND d.start_time >= t.start_time AND d.end_time <= t.end_time
			WHERE d.server = ? AND d.source_category IN (?)
		)
		SELECT 'times' AS kind, source_category, stage_id, item_id, 0 AS key, SUM(times) AS value
		FROM (SELECT DISTINCT source_category, stage_id, item_id, times, day_num FROM e WHERE times > 0) AS distinct_times
		GROUP BY source_category, stage_id, item_id
		UNION ALL
		SELECT 'quantity', source_category, stage_id, item_id, 0, SUM(quantity)
		FROM e WHERE quantity > 0
		GROUP BY source_category, stage_id, item_id
		UNION ALL
		SELECT 'bucket', source_category, stage_id, item_id, b.key::int, SUM(b.value::numeric)::bigint
		FROM e, jsonb_each_text(e.quantity_buckets) AS b
		WHERE quantity > 0
		GROUP BY source_category, stage_id, item_id, b.key`,
		pgdialect.Array(stageIds), pgdialect.Array(itemIds), pgdialect.Array(starts), pgdialect.Array(ends),
		server, bun.In(sourceCategories)).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		key := model.GlobalDropMatrixKey{SourceCategory: row.SourceCategory, StageID: row.StageID, ItemID: row.ItemID}
		aggregate, ok := aggregates[key]
		if !ok {
			aggregate = &model.GlobalDropMatrixAggregate{QuantityBuckets: make(map[int]int)}
			aggregates[key] = aggregate
		}
		switch row.Kind {
		case "times":
			aggregate.Times = null.IntFrom(row.Value)
		case "quantity":
			aggregate.Quantity = null.IntFrom(row.Value)
		case "bucket":
			aggregate.QuantityBuckets[row.Key] = int(row.Value)
		}
	}
	return aggregates, nil
}

// CalcSiteStatsAggregates aggregates the drop matrix elements of a server (source category "all") with
// fromDayNum <= day_num < toDayNum per stage and item, see model.SiteStatsAggregate. Invalid bounds are open.
func (s *DropMatrixElement) CalcSiteStatsAggregates(
	ctx context.Context, server string, fromDayNum, toDayNum null.Int,
) ([]*model.SiteStatsAggregate, error) {
	elements := s.db.NewSelect().
		TableExpr("drop_matrix_elements").
		Column("stage_id", "item_id", "times", "day_num").
		ColumnExpr("SUM(quantity) AS quantity").
		Where("server = ?", server).
		Where("source_category = ?", constant.SourceCategoryAll).
		Group("stage_id", "item_id", "times", "day_num")
	if fromDayNum.Valid {
		elements = elements.Where("day_num >= ?", fromDayNum.Int64)
	}
	if toDayNum.Valid {
		elements = elements.Where("day_num < ?", toDayNum.Int64)
	}

	results := make([]*model.SiteStatsAggregate, 0)
	err := s.db.NewSelect().
		TableExpr("(?) AS g", elements).
		Column("stage_id", "item_id").
		ColumnExpr("SUM(times) FILTER (WHERE times > 0) AS times").
		ColumnExpr("SUM(quantity) AS quantity").
		Group("stage_id", "item_id").
		Scan(ctx, &results)
	if err != nil {
		return nil, err
	}
	return results, nil
}
