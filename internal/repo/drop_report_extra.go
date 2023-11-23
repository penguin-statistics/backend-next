package repo

import (
	"context"

	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo/selector"
)

type DropReportExtra struct {
	db  *bun.DB
	sel selector.S[model.DropReportExtra]
}

func NewDropReportExtra(db *bun.DB) *DropReportExtra {
	return &DropReportExtra{db: db, sel: selector.New[model.DropReportExtra](db)}
}

func (r *DropReportExtra) GetDropReportExtraById(ctx context.Context, id int) (*model.DropReportExtra, error) {
	return r.sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("report_id = ?", id)
	})
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

// DeleteDropReportExtrasForArchive deletes all drop report extras with report_id between idInclusiveStart and idInclusiveEnd.
// Returns the number of rows affected and an error if any.
func (c *DropReportExtra) DeleteDropReportExtrasForArchive(ctx context.Context, tx bun.Tx, idInclusiveStart int, idInclusiveEnd int) (int64, error) {
	r, err := tx.NewDelete().
		Model((*model.DropReportExtra)(nil)).
		Where("report_id >= ?", idInclusiveStart).
		Where("report_id <= ?", idInclusiveEnd).
		Exec(ctx)
	if err != nil {
		return -1, err
	}

	return r.RowsAffected()
}

func (c *DropReportExtra) IsDropReportExtraMD5Exist(ctx context.Context, md5 string) bool {
	var dropReportExtra model.DropReportExtra

	count, err := c.db.NewSelect().
		Model(&dropReportExtra).
		Where("md5 = ?", md5).
		Count(ctx)
	if err != nil {
		return false
	}

	return count > 0
}

func (r *DropReportExtra) CreateDropReportExtra(ctx context.Context, tx bun.Tx, report *model.DropReportExtra) error {
	_, err := tx.NewInsert().
		Model(report).
		Exec(ctx)

	return err
}
