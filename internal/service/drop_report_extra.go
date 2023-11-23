package service

import (
	"context"

	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo"
)

type DropReportExtra struct {
	DropReportExtraRepo *repo.DropReportExtra
}

func NewDropReportExtra(dropReportExtraRepo *repo.DropReportExtra) *DropReportExtra {
	return &DropReportExtra{
		DropReportExtraRepo: dropReportExtraRepo,
	}
}

func (s *DropReportExtra) GetDropReportExtraForArchive(ctx context.Context, cursor *model.Cursor, idInclusiveStart int, idInclusiveEnd int, limit int) ([]*model.DropReportExtra, model.Cursor, error) {
	return s.DropReportExtraRepo.GetDropReportExtraForArchive(ctx, cursor, idInclusiveStart, idInclusiveEnd, limit)
}

func (c *DropReportExtra) DeleteDropReportExtrasForArchive(ctx context.Context, tx bun.Tx, idInclusiveStart int, idInclusiveEnd int) (int64, error) {
	return c.DropReportExtraRepo.DeleteDropReportExtrasForArchive(ctx, tx, idInclusiveStart, idInclusiveEnd)
}
