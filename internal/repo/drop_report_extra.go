package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
)

type DropReportExtra struct {
	DB *bun.DB
}

func NewDropReportExtra(db *bun.DB) *DropReportExtra {
	return &DropReportExtra{DB: db}
}

func (c *DropReportExtra) GetDropReportExtraById(ctx context.Context, id int) (*model.DropReportExtra, error) {
	var dropReportExtra model.DropReportExtra

	err := c.DB.NewSelect().
		Model(&dropReportExtra).
		Where("report_id = ?", id).
		Scan(ctx)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, pgerr.ErrNotFound
	} else if err != nil {
		return nil, err
	}

	return &dropReportExtra, nil
}

func (c *DropReportExtra) GetDropReportExtraForArchive(ctx context.Context, cursor *model.Cursor, idInclusiveStart int, idInclusiveEnd int, limit int) ([]*model.DropReportExtra, model.Cursor, error) {
	dropReportExtras := make([]*model.DropReportExtra, 0)

	query := c.DB.NewSelect().
		Model(&dropReportExtras).
		Where("report_id >= ?", idInclusiveStart).
		Where("report_id <= ?", idInclusiveEnd).
		Order("report_id").
		Limit(limit)
	if cursor != nil && cursor.Start > 0 {
		query = query.Where("report_id > ?", cursor.Start)
	}

	err := query.Scan(ctx)
	if err != nil {
		return nil, model.Cursor{}, err
	}

	var newCursor model.Cursor
	if len(dropReportExtras) == 0 {
		newCursor = model.Cursor{}
	} else {
		newCursor = model.Cursor{
			Start: dropReportExtras[0].ReportID,
			End:   dropReportExtras[len(dropReportExtras)-1].ReportID,
		}
	}

	return dropReportExtras, newCursor, nil
}

func (c *DropReportExtra) DeleteDropReportExtrasForArchive(ctx context.Context, tx bun.Tx, idInclusiveStart int, idInclusiveEnd int) error {
	_, err := tx.NewDelete().
		Model((*model.DropReportExtra)(nil)).
		Where("report_id >= ?", idInclusiveStart).
		Where("report_id <= ?", idInclusiveEnd).
		Exec(ctx)

	return err
}

func (c *DropReportExtra) IsDropReportExtraMD5Exist(ctx context.Context, md5 string) bool {
	var dropReportExtra model.DropReportExtra

	count, err := c.DB.NewSelect().
		Model(&dropReportExtra).
		Where("md5 = ?", md5).
		Count(ctx)
	if err != nil {
		return false
	}

	return count > 0
}

func (c *DropReportExtra) CreateDropReportExtra(ctx context.Context, tx bun.Tx, report *model.DropReportExtra) error {
	_, err := tx.NewInsert().
		Model(report).
		Exec(ctx)

	return err
}
