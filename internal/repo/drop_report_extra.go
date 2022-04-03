package repo

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"

	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
)

type DropReportExtraRepo struct {
	DB *bun.DB
}

func NewDropReportExtraRepo(db *bun.DB) *DropReportExtraRepo {
	return &DropReportExtraRepo{DB: db}
}

func (c *DropReportExtraRepo) GetDropReportExtraById(ctx context.Context, id int) (*models.DropReportExtra, error) {
	var dropReportExtra models.DropReportExtra

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

func (c *DropReportExtraRepo) IsDropReportExtraMD5Exist(ctx context.Context, md5 string) bool {
	var dropReportExtra models.DropReportExtra

	count, err := c.DB.NewSelect().
		Model(&dropReportExtra).
		Where("md5 = ?", md5).
		Count(ctx)
	if err != nil {
		return false
	}

	return count > 0
}

func (c *DropReportExtraRepo) CreateDropReportExtra(ctx context.Context, tx bun.Tx, report *models.DropReportExtra) error {
	_, err := tx.NewInsert().
		Model(report).
		Exec(ctx)

	return err
}
