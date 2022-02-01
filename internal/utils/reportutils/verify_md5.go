package reportutils

import (
	"context"
	"errors"

	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type MD5Verifier struct {
	DropReportExtraRepo *repos.DropReportExtraRepo
}

func NewMD5Verifier(dropReportExtraRepo *repos.DropReportExtraRepo) *MD5Verifier {
	return &MD5Verifier{
		DropReportExtraRepo: dropReportExtraRepo,
	}
}

func (u *MD5Verifier) Verify(ctx context.Context, report *types.SingleReport, reportTask *types.ReportTask) error {
	if report.Metadata != nil && u.DropReportExtraRepo.IsDropReportExtraMD5Exist(ctx, report.Metadata.MD5) {
		return errors.New("report with md5 already exist")
	}
	return nil
}
