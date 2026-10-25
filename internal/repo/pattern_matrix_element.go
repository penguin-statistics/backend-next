package repo

import (
	"context"
	"time"

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

// CalcGlobalPatternMatrixAggregates aggregates the elements of every target of server for the source categories in
// a single scan, see model.GlobalPatternMatrixAggregates. A single statement also reads a single snapshot, unaffected
// by a concurrent rewrite (delete, then insert) of the elements.
func (s *PatternMatrixElement) CalcGlobalPatternMatrixAggregates(
	ctx context.Context, server string, targets []*model.GlobalPatternMatrixTarget, sourceCategories []string,
) (map[string]*model.GlobalPatternMatrixAggregates, error) {
	aggregates := make(map[string]*model.GlobalPatternMatrixAggregates, len(sourceCategories))
	for _, sourceCategory := range sourceCategories {
		aggregates[sourceCategory] = &model.GlobalPatternMatrixAggregates{
			TimesByStageID:      make(map[int]int),
			QuantitiesByStageID: make(map[int]map[int]int),
		}
	}
	if len(targets) == 0 || len(sourceCategories) == 0 {
		return aggregates, nil
	}
	stageIds := make([]int, len(targets))
	// compared at second precision, as the time ranges were formatted as RFC 3339 before
	starts := make([]time.Time, len(targets))
	ends := make([]time.Time, len(targets))
	for i, target := range targets {
		stageIds[i] = target.StageID
		starts[i] = time.Unix(target.TimeRange.StartTime.Unix(), 0)
		ends[i] = time.Unix(target.TimeRange.EndTime.Unix(), 0)
	}

	var rows []struct {
		Kind           string `bun:"kind"`
		SourceCategory string `bun:"source_category"`
		StageID        int    `bun:"stage_id"`
		PatternID      int    `bun:"pattern_id"`
		Value          int    `bun:"value"`
	}
	err := s.db.NewRaw(`WITH t AS (
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
		pgdialect.Array(stageIds), pgdialect.Array(starts), pgdialect.Array(ends), server, bun.In(sourceCategories)).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		aggregate := aggregates[row.SourceCategory]
		if row.Kind == "times" {
			aggregate.TimesByStageID[row.StageID] = row.Value
			continue
		}
		if _, ok := aggregate.QuantitiesByStageID[row.StageID]; !ok {
			aggregate.QuantitiesByStageID[row.StageID] = make(map[int]int)
		}
		aggregate.QuantitiesByStageID[row.StageID][row.PatternID] = row.Value
	}
	return aggregates, nil
}
