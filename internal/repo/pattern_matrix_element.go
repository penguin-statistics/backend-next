package repo

import (
	"context"
	"database/sql"
	"time"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
)

type PatternMatrixElement struct {
	db *bun.DB
}

func NewPatternMatrixElement(db *bun.DB) *PatternMatrixElement {
	return &PatternMatrixElement{db: db}
}

func (s *PatternMatrixElement) BatchSaveElements(ctx context.Context, elements []*model.PatternMatrixElement, server string) error {
	_, err := s.db.NewInsert().Model(&elements).Exec(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (s *PatternMatrixElement) DeleteByServer(ctx context.Context, server string) error {
	_, err := s.db.NewDelete().Model((*model.PatternMatrixElement)(nil)).Where("server = ?", server).Exec(ctx)
	return err
}

func (s *PatternMatrixElement) DeleteByServerAndDayNum(ctx context.Context, server string, dayNum int) error {
	_, err := s.db.NewDelete().Model((*model.PatternMatrixElement)(nil)).Where("server = ?", server).Where("day_num = ?", dayNum).Exec(ctx)
	return err
}

func (s *PatternMatrixElement) GetElementsByServerAndSourceCategoryAndStartAndEndTime(
	ctx context.Context, server string, sourceCategory string, start *time.Time, end *time.Time,
) ([]*model.PatternMatrixElement, error) {
	var elements []*model.PatternMatrixElement
	startTimeStr := start.Format(time.RFC3339)
	endTimeStr := end.Format(time.RFC3339)
	err := s.db.NewSelect().Model(&elements).
		Where("server = ?", server).
		Where("source_category = ?", sourceCategory).
		Where("start_time >= timestamp with time zone ?", startTimeStr).
		Where("end_time <= timestamp with time zone ?", endTimeStr).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return elements, nil
}

func (s *PatternMatrixElement) GetElementsByServerAndSourceCategory(ctx context.Context, server string, sourceCategory string) ([]*model.PatternMatrixElement, error) {
	var elements []*model.PatternMatrixElement
	err := s.db.NewSelect().Model(&elements).Where("server = ?", server).Where("source_category = ?", sourceCategory).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return elements, nil
}

/**
 * @param startDayNum inclusive
 * @param endDayNum inclusive
 */
func (s *PatternMatrixElement) GetElementsByServerAndSourceCategoryAndDayNumRange(
	ctx context.Context, server string, sourceCategory string, startDayNum int, endDayNum int,
) ([]*model.PatternMatrixElement, error) {
	var elements []*model.PatternMatrixElement
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

func (s *PatternMatrixElement) IsExistByServerAndDayNum(ctx context.Context, server string, dayNum int) (bool, error) {
	exists, err := s.db.NewSelect().Model((*model.PatternMatrixElement)(nil)).Where("server = ?", server).Where("day_num = ?", dayNum).Exists(ctx)
	if err != nil {
		return false, err
	}
	return exists, nil
}
