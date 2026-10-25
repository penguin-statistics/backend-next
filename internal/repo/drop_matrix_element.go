package repo

import (
	"context"
	"database/sql"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/pkg/errors"
	"github.com/samber/lo"
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

// ReplaceDay replaces the elements of server on dayNum by elements, all of that day, atomically (see
// replaceElementsOfDay).
func (s *DropMatrixElement) ReplaceDay(ctx context.Context, server string, dayNum int, elements []*model.DropMatrixElement) error {
	return replaceElementsOfDay(ctx, s.db, "drop_matrix_elements", server, dayNum, elements,
		func(el *model.DropMatrixElement) (string, int) { return el.Server, el.DayNum })
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

// DB returns the database the methods of s read and write by default: that to pass to a method taking a bun.IDB
// outside of a transaction.
func (s *DropMatrixElement) DB() bun.IDB {
	return s.db
}

// RunInSnapshotTx runs fn in a read-only snapshot transaction of the database of s, see RunInSnapshotTx.
func (s *DropMatrixElement) RunInSnapshotTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error {
	return RunInSnapshotTx(ctx, s.db, fn)
}

// globalDropMatrixAggregateRow is a row of the global drop matrix aggregations: a part (kind) of the aggregate of a
// target in a source category, of the days before the split or of the others if the aggregation splits them.
type globalDropMatrixAggregateRow struct {
	Kind           string `bun:"kind"`
	Before         bool   `bun:"before"`
	SourceCategory string `bun:"source_category"`
	StageID        int    `bun:"stage_id"`
	ItemID         int    `bun:"item_id"`
	Key            int    `bun:"key"`
	Value          int64  `bun:"value"`
}

// globalDropMatrixTargetArgs returns the stages, items, starts and ends of targets as arrays, for unnest.
func globalDropMatrixTargetArgs(targets []*model.GlobalDropMatrixTarget) []interface{} {
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
	return []interface{}{pgdialect.Array(stageIds), pgdialect.Array(itemIds), pgdialect.Array(starts), pgdialect.Array(ends)}
}

// addGlobalDropMatrixAggregateRow sets the part of row in its aggregate in aggregates.
func addGlobalDropMatrixAggregateRow(aggregates map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate, row *globalDropMatrixAggregateRow) {
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

// CalcGlobalDropMatrixAggregates aggregates the elements of every target of server for the source categories in a
// single scan, see model.GlobalDropMatrixAggregate. A single statement also reads a single snapshot, unaffected by a
// concurrent rewrite (delete, then insert) of the elements. It reads db, which may be a transaction.
func (s *DropMatrixElement) CalcGlobalDropMatrixAggregates(
	ctx context.Context, db bun.IDB, server string, targets []*model.GlobalDropMatrixTarget, sourceCategories []string,
) (map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate, error) {
	aggregates := make(map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate)
	if len(targets) == 0 || len(sourceCategories) == 0 {
		return aggregates, nil
	}

	var rows []globalDropMatrixAggregateRow
	err := db.NewRaw(`WITH t AS (
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
		append(globalDropMatrixTargetArgs(targets), server, bun.In(sourceCategories))...).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	for i := range rows {
		addGlobalDropMatrixAggregateRow(aggregates, &rows[i])
	}
	return aggregates, nil
}

// CalcGlobalDropMatrixPartials aggregates the elements of the targets like CalcGlobalDropMatrixAggregates, only those
// in scope, and those of the days before scope.SplitDayNum (before) apart from the others (after). For each key,
// before Add after is the aggregate CalcGlobalDropMatrixAggregates gives for the elements in scope in the same
// snapshot (see model.GlobalDropMatrixAggregate.Add); a key with nothing on a side is missing from it.
func (s *DropMatrixElement) CalcGlobalDropMatrixPartials(
	ctx context.Context, db bun.IDB, server string, targets []*model.GlobalDropMatrixTarget, sourceCategories []string,
	scope model.GlobalMatrixScope,
) (before, after map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate, err error) {
	before = make(map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate)
	after = make(map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate)
	if len(targets) == 0 || len(sourceCategories) == 0 {
		return before, after, nil
	}

	args := append(globalDropMatrixTargetArgs(targets), scope.SplitDayNum)
	elements := "drop_matrix_elements"
	if scope.StageFirst {
		// OFFSET 0 keeps the subquery from being flattened: the elements of the stages are hash joined with the
		// targets, instead of being looked up target by target in a nested loop
		elements = "(SELECT * FROM drop_matrix_elements WHERE server = ? AND stage_id = ANY(?::int[]) OFFSET 0)"
		args = append(args, server, pgdialect.Array(lo.Uniq(lo.Map(targets, func(target *model.GlobalDropMatrixTarget, _ int) int {
			return target.StageID
		}))))
	}
	args = append(args, server, bun.In(sourceCategories))
	fromDayNum := ""
	if scope.FromDayNum.Valid {
		fromDayNum = " AND d.day_num >= ?"
		args = append(args, scope.FromDayNum.Int64)
	}

	// that of CalcGlobalDropMatrixAggregates, with the side of the split as one more grouping column; a distinct
	// (times, day_num) is on one side only
	var rows []globalDropMatrixAggregateRow
	err = db.NewRaw(`WITH t AS (
			SELECT * FROM unnest(?::int[], ?::int[], ?::timestamptz[], ?::timestamptz[]) AS t(stage_id, item_id, start_time, end_time)
		), e AS MATERIALIZED (
			SELECT d.source_category, d.stage_id, d.item_id, d.times, d.day_num, d.quantity, d.quantity_buckets, d.day_num < ? AS before
			FROM `+elements+` AS d
			JOIN t ON d.stage_id = t.stage_id AND d.item_id = t.item_id AND d.start_time >= t.start_time AND d.end_time <= t.end_time
			WHERE d.server = ? AND d.source_category IN (?)`+fromDayNum+`
		)
		SELECT 'times' AS kind, before, source_category, stage_id, item_id, 0 AS key, SUM(times) AS value
		FROM (SELECT DISTINCT before, source_category, stage_id, item_id, times, day_num FROM e WHERE times > 0) AS distinct_times
		GROUP BY before, source_category, stage_id, item_id
		UNION ALL
		SELECT 'quantity', before, source_category, stage_id, item_id, 0, SUM(quantity)
		FROM e WHERE quantity > 0
		GROUP BY before, source_category, stage_id, item_id
		UNION ALL
		SELECT 'bucket', before, source_category, stage_id, item_id, b.key::int, SUM(b.value::numeric)::bigint
		FROM e, jsonb_each_text(e.quantity_buckets) AS b
		WHERE quantity > 0
		GROUP BY before, source_category, stage_id, item_id, b.key`,
		args...).
		Scan(ctx, &rows)
	if err != nil {
		return nil, nil, err
	}

	for i := range rows {
		if rows[i].Before {
			addGlobalDropMatrixAggregateRow(before, &rows[i])
		} else {
			addGlobalDropMatrixAggregateRow(after, &rows[i])
		}
	}
	return before, after, nil
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
