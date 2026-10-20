package repo

import (
	"context"
	"database/sql"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/pkg/errors"
	"github.com/uptrace/bun"
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

func (s *DropMatrixElement) GetAllTimesForGlobalDropMatrix(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIds []int, sourceCategory string,
) ([]*model.AllTimesResultForGlobalDropMatrix, error) {
	subq2 := s.db.NewSelect().
		TableExpr("drop_matrix_elements").
		Column("stage_id", "item_id", "times", "day_num").
		Where("server = ?", server).
		Where("source_category = ?", sourceCategory).
		Where("times > 0").
		Where("stage_id IN (?)", bun.In(stageIds)).
		Where("start_time >= timestamp with time zone ?", timeRange.StartTime.Format(time.RFC3339)).
		Where("end_time <= timestamp with time zone ?", timeRange.EndTime.Format(time.RFC3339))

	subq1 := s.db.NewSelect().
		TableExpr("(?) AS subq2", subq2).
		Column("stage_id", "item_id", "times").
		Group("stage_id", "item_id", "times", "day_num")

	mainq := s.db.NewSelect().
		TableExpr("(?) AS subq1", subq1).
		Column("stage_id", "item_id").
		ColumnExpr("SUM(times) AS times").
		Group("stage_id", "item_id")

	results := make([]*model.AllTimesResultForGlobalDropMatrix, 0)
	err := mainq.Scan(ctx, &results)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropMatrixElement) GetAllQuantitiesForGlobalDropMatrix(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIds []int, sourceCategory string,
) ([]*model.AllQuantitiesResultForGlobalDropMatrix, error) {
	subq1 := s.db.NewSelect().
		TableExpr("drop_matrix_elements").
		Column("stage_id", "item_id", "quantity").
		Where("server = ?", server).
		Where("source_category = ?", sourceCategory).
		Where("quantity > 0").
		Where("stage_id IN (?)", bun.In(stageIds)).
		Where("start_time >= timestamp with time zone ?", timeRange.StartTime.Format(time.RFC3339)).
		Where("end_time <= timestamp with time zone ?", timeRange.EndTime.Format(time.RFC3339))

	mainq := s.db.NewSelect().
		TableExpr("(?) AS subq1", subq1).
		Column("stage_id", "item_id").
		ColumnExpr("SUM(quantity) AS quantity").
		Group("stage_id", "item_id")

	results := make([]*model.AllQuantitiesResultForGlobalDropMatrix, 0)
	err := mainq.Scan(ctx, &results)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropMatrixElement) GetAllQuantityBucketsForGlobalDropMatrix(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIds []int, sourceCategory string,
) ([]*model.AllQuantityBucketsResultForGlobalDropMatrix, error) {
	subq2 := s.db.NewSelect().
		TableExpr("drop_matrix_elements").
		Column("stage_id", "item_id", "quantity_buckets").
		Where("server = ?", server).
		Where("source_category = ?", sourceCategory).
		Where("quantity > 0").
		Where("stage_id IN (?)", bun.In(stageIds)).
		Where("start_time >= timestamp with time zone ?", timeRange.StartTime.Format(time.RFC3339)).
		Where("end_time <= timestamp with time zone ?", timeRange.EndTime.Format(time.RFC3339))

	subq1 := s.db.NewSelect().
		TableExpr("(?) AS subq2", subq2).
		TableExpr("jsonb_each_text(quantity_buckets)").
		Column("stage_id", "item_id", "key").
		ColumnExpr("sum(value::numeric) val").
		Group("stage_id", "item_id", "key")

	mainq := s.db.NewSelect().
		TableExpr("(?) AS subq1", subq1).
		Column("stage_id", "item_id").
		ColumnExpr("json_object_agg(key, val) AS quantity_buckets").
		Group("stage_id", "item_id")

	results := make([]*model.AllQuantityBucketsResultForGlobalDropMatrix, 0)
	err := mainq.Scan(ctx, &results)
	if err != nil {
		return nil, err
	}
	return results, nil
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
