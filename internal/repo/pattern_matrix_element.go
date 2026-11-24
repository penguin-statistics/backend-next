package repo

import (
	"context"
	"time"

	"github.com/samber/lo"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo/selector"
)

type PatternMatrixElement struct {
	db  *bun.DB
	sel selector.S[model.PatternMatrixElement]
}

func NewPatternMatrixElement(db *bun.DB) *PatternMatrixElement {
	return &PatternMatrixElement{db: db, sel: selector.New[model.PatternMatrixElement](db)}
}

// ReplaceDay replaces the elements of server on dayNum by elements, all of that day, atomically (see
// replaceElementsOfDay).
func (s *PatternMatrixElement) ReplaceDay(ctx context.Context, server string, dayNum int, elements []*model.PatternMatrixElement) error {
	return replaceElementsOfDay(ctx, s.db, "pattern_matrix_elements", server, dayNum, elements,
		func(el *model.PatternMatrixElement) (string, int) { return el.Server, el.DayNum })
}

func (s *PatternMatrixElement) IsExistByServerAndDayNum(ctx context.Context, server string, dayNum int) (bool, error) {
	exists, err := s.db.NewSelect().Model((*model.PatternMatrixElement)(nil)).Where("server = ?", server).Where("day_num = ?", dayNum).Exists(ctx)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// DB returns the database the methods of s read and write by default: that to pass to a method taking a bun.IDB
// outside of a transaction.
func (s *PatternMatrixElement) DB() bun.IDB {
	return s.db
}

// RunInSnapshotTx runs fn in a read-only snapshot transaction of the database of s, see RunInSnapshotTx.
func (s *PatternMatrixElement) RunInSnapshotTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error {
	return RunInSnapshotTx(ctx, s.db, fn)
}

// globalPatternMatrixAggregateRow is a row of the global pattern matrix aggregations: the times of a stage, or the
// quantity of a stage and pattern, in a source category, of the days before the split or of the others if the
// aggregation splits them.
type globalPatternMatrixAggregateRow struct {
	Kind           string `bun:"kind"`
	Before         bool   `bun:"before"`
	SourceCategory string `bun:"source_category"`
	StageID        int    `bun:"stage_id"`
	PatternID      int    `bun:"pattern_id"`
	Value          int    `bun:"value"`
}

// globalPatternMatrixTargetArgs returns the stages, starts and ends of targets as arrays, for unnest.
func globalPatternMatrixTargetArgs(targets []*model.GlobalPatternMatrixTarget) []interface{} {
	stageIds := make([]int, len(targets))
	// compared at second precision, as the time ranges were formatted as RFC 3339 before
	starts := make([]time.Time, len(targets))
	ends := make([]time.Time, len(targets))
	for i, target := range targets {
		stageIds[i] = target.StageID
		starts[i] = time.Unix(target.TimeRange.StartTime.Unix(), 0)
		ends[i] = time.Unix(target.TimeRange.EndTime.Unix(), 0)
	}
	return []interface{}{pgdialect.Array(stageIds), pgdialect.Array(starts), pgdialect.Array(ends)}
}

// newGlobalPatternMatrixAggregates returns empty aggregates for every source category.
func newGlobalPatternMatrixAggregates(sourceCategories []string) map[string]*model.GlobalPatternMatrixAggregates {
	aggregates := make(map[string]*model.GlobalPatternMatrixAggregates, len(sourceCategories))
	for _, sourceCategory := range sourceCategories {
		aggregates[sourceCategory] = &model.GlobalPatternMatrixAggregates{
			TimesByStageID:      make(map[int]int),
			QuantitiesByStageID: make(map[int]map[int]int),
		}
	}
	return aggregates
}

// addGlobalPatternMatrixAggregateRow sets the times or quantity of row in the aggregates of its source category.
func addGlobalPatternMatrixAggregateRow(aggregates map[string]*model.GlobalPatternMatrixAggregates, row *globalPatternMatrixAggregateRow) {
	aggregate := aggregates[row.SourceCategory]
	if row.Kind == "times" {
		aggregate.TimesByStageID[row.StageID] = row.Value
		return
	}
	if _, ok := aggregate.QuantitiesByStageID[row.StageID]; !ok {
		aggregate.QuantitiesByStageID[row.StageID] = make(map[int]int)
	}
	aggregate.QuantitiesByStageID[row.StageID][row.PatternID] = row.Value
}

// CalcGlobalPatternMatrixAggregates aggregates the elements of every target of server for the source categories in
// a single scan, see model.GlobalPatternMatrixAggregates. A single statement also reads a single snapshot, unaffected
// by a concurrent rewrite (delete, then insert) of the elements. It reads db, which may be a transaction.
func (s *PatternMatrixElement) CalcGlobalPatternMatrixAggregates(
	ctx context.Context, db bun.IDB, server string, targets []*model.GlobalPatternMatrixTarget, sourceCategories []string,
) (map[string]*model.GlobalPatternMatrixAggregates, error) {
	aggregates := newGlobalPatternMatrixAggregates(sourceCategories)
	if len(targets) == 0 || len(sourceCategories) == 0 {
		return aggregates, nil
	}

	var rows []globalPatternMatrixAggregateRow
	err := db.NewRaw(`WITH t AS (
			SELECT * FROM unnest(?::int[], ?::timestamptz[], ?::timestamptz[]) AS t(stage_id, start_time, end_time)
		), e AS MATERIALIZED (
			SELECT p.source_category, p.stage_id, p.pattern_id, p.times, p.day_num, p.quantity
			FROM pattern_matrix_elements AS p
			JOIN t ON p.stage_id = t.stage_id AND p.start_time >= t.start_time AND p.end_time <= t.end_time
			WHERE p.server = ? AND p.source_category IN (?)
		)
		SELECT 'times' AS kind, source_category, stage_id, 0 AS pattern_id, SUM(times) AS value
		FROM (SELECT source_category, stage_id, MIN(times) AS times FROM e WHERE times > 0 GROUP BY source_category, stage_id, day_num) AS daily
		GROUP BY source_category, stage_id
		UNION ALL
		SELECT 'quantity', source_category, stage_id, pattern_id, SUM(quantity)
		FROM e WHERE quantity > 0
		GROUP BY source_category, stage_id, pattern_id`,
		append(globalPatternMatrixTargetArgs(targets), server, bun.In(sourceCategories))...).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	for i := range rows {
		addGlobalPatternMatrixAggregateRow(aggregates, &rows[i])
	}
	return aggregates, nil
}

// CalcGlobalPatternMatrixPartials aggregates the elements of the targets like CalcGlobalPatternMatrixAggregates, only
// those in scope, and those of the days before scope.SplitDayNum (before) apart from the others (after). For each
// source category, before Add after is the aggregates CalcGlobalPatternMatrixAggregates gives for the elements in
// scope in the same snapshot (see model.GlobalPatternMatrixAggregates.Add).
func (s *PatternMatrixElement) CalcGlobalPatternMatrixPartials(
	ctx context.Context, db bun.IDB, server string, targets []*model.GlobalPatternMatrixTarget, sourceCategories []string,
	scope model.GlobalMatrixScope,
) (before, after map[string]*model.GlobalPatternMatrixAggregates, err error) {
	before = newGlobalPatternMatrixAggregates(sourceCategories)
	after = newGlobalPatternMatrixAggregates(sourceCategories)
	if len(targets) == 0 || len(sourceCategories) == 0 {
		return before, after, nil
	}

	args := append(globalPatternMatrixTargetArgs(targets), scope.SplitDayNum)
	elements := "pattern_matrix_elements"
	if scope.StageFirst {
		// as for the global drop matrices (DropMatrixElement.CalcGlobalDropMatrixPartials)
		elements = "(SELECT * FROM pattern_matrix_elements WHERE server = ? AND stage_id = ANY(?::int[]) OFFSET 0)"
		args = append(args, server, pgdialect.Array(lo.Uniq(lo.Map(targets, func(target *model.GlobalPatternMatrixTarget, _ int) int {
			return target.StageID
		}))))
	}
	args = append(args, server, bun.In(sourceCategories))
	fromDayNum := ""
	if scope.FromDayNum.Valid {
		fromDayNum = " AND p.day_num >= ?"
		args = append(args, scope.FromDayNum.Int64)
	}

	// that of CalcGlobalPatternMatrixAggregates, with the side of the split as one more grouping column; the
	// smallest times of a day is on one side only
	var rows []globalPatternMatrixAggregateRow
	err = db.NewRaw(`WITH t AS (
			SELECT * FROM unnest(?::int[], ?::timestamptz[], ?::timestamptz[]) AS t(stage_id, start_time, end_time)
		), e AS MATERIALIZED (
			SELECT p.source_category, p.stage_id, p.pattern_id, p.times, p.day_num, p.quantity, p.day_num < ? AS before
			FROM `+elements+` AS p
			JOIN t ON p.stage_id = t.stage_id AND p.start_time >= t.start_time AND p.end_time <= t.end_time
			WHERE p.server = ? AND p.source_category IN (?)`+fromDayNum+`
		)
		SELECT 'times' AS kind, before, source_category, stage_id, 0 AS pattern_id, SUM(times) AS value
		FROM (SELECT before, source_category, stage_id, MIN(times) AS times FROM e WHERE times > 0 GROUP BY before, source_category, stage_id, day_num) AS daily
		GROUP BY before, source_category, stage_id
		UNION ALL
		SELECT 'quantity', before, source_category, stage_id, pattern_id, SUM(quantity)
		FROM e WHERE quantity > 0
		GROUP BY before, source_category, stage_id, pattern_id`,
		args...).
		Scan(ctx, &rows)
	if err != nil {
		return nil, nil, err
	}

	for i := range rows {
		if rows[i].Before {
			addGlobalPatternMatrixAggregateRow(before, &rows[i])
		} else {
			addGlobalPatternMatrixAggregateRow(after, &rows[i])
		}
	}
	return before, after, nil
}
