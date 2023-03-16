package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
)

type DropMatrixElement struct {
	db *bun.DB
}

func NewDropMatrixElement(db *bun.DB) *DropMatrixElement {
	return &DropMatrixElement{db: db}
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
 * @param startDayNum inclusive
 * @param endDayNum inclusive
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

func (s *DropMatrixElement) GetAllTimesForGlobalDropMatrix(ctx context.Context, server string, sourceCategory string) ([]*model.AllTimesResultForGlobalDropMatrix, error) {
	subq2 := s.db.NewSelect().
		TableExpr("drop_matrix_elements").
		Column("stage_id", "times", "day_num").
		Where("server = ?", server).
		Where("source_category = ?", sourceCategory).
		Where("times > 0")

	subq1 := s.db.NewSelect().
		TableExpr("(?) AS subq2", subq2).
		Column("stage_id", "times", "day_num").
		Group("stage_id", "times", "day_num")

	mainq := s.db.NewSelect().
		TableExpr("(?) AS subq1", subq1).
		Column("stage_id").
		ColumnExpr("SUM(times) AS times").
		ColumnExpr("min(day_num) AS day_num_min").
		ColumnExpr("max(day_num) AS day_num_max").
		Group("stage_id")

	results := make([]*model.AllTimesResultForGlobalDropMatrix, 0)
	err := mainq.Scan(ctx, &results)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropMatrixElement) GetAllQuantitiesForGlobalDropMatrix(ctx context.Context, server string, sourceCategory string) ([]*model.AllQuantitiesResultForGlobalDropMatrix, error) {
	subq1 := s.db.NewSelect().
		TableExpr("drop_matrix_elements").
		Column("stage_id", "item_id", "quantity").
		Where("server = ?", server).
		Where("source_category = ?", sourceCategory).
		Where("quantity > 0")

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

func (s *DropMatrixElement) GetAllQuantityBucketsForGlobalDropMatrix(ctx context.Context, server string, sourceCategory string) ([]*model.AllQuantityBucketsResultForGlobalDropMatrix, error) {
	subq2 := s.db.NewSelect().
		TableExpr("drop_matrix_elements").
		Column("stage_id", "item_id", "quantity_buckets").
		Where("server = ?", server).
		Where("source_category = ?", sourceCategory).
		Where("quantity > 0")

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
