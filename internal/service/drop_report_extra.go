package service

import (
	"context"

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
