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

// CreateDropReportExtra inserts the extra of a report, with created_at only when report.CreatedAt is set (see
// model.DropReportExtra.CreatedAt).
func (r *DropReportExtra) CreateDropReportExtra(ctx context.Context, tx bun.Tx, report *model.DropReportExtra) error {
	query := tx.NewInsert().
		Model(report)
	if report.CreatedAt != nil {
		query = query.Value("created_at", "?", *report.CreatedAt)
	}
	_, err := query.Exec(ctx)

	return err
}
