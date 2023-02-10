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

func (c *DropReportExtra) GetDropReportExtraById(ctx context.Context, id int) (*model.DropReportExtra, error) {
	return c.sel.SelectOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("report_id = ?", id)
	})
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

func (c *DropReportExtra) CreateDropReportExtra(ctx context.Context, tx bun.Tx, report *model.DropReportExtra) error {
	_, err := tx.NewInsert().
		Model(report).
		Exec(ctx)

	return err
}
