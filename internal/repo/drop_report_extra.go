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
