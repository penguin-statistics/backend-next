package reportutils

import (
	"context"

	"github.com/pkg/errors"

	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

var ErrMD5Conflict = errors.New("report with specified md5 has already exited")

type MD5Verifier struct {
	DropReportExtraRepo *repos.DropReportExtraRepo
}

func NewMD5Verifier(dropReportExtraRepo *repos.DropReportExtraRepo) *MD5Verifier {
	return &MD5Verifier{
		DropReportExtraRepo: dropReportExtraRepo,
	}
}

func (u *MD5Verifier) Name() string {
	return "md5"
}

func (u *MD5Verifier) Verify(ctx context.Context, report *types.SingleReport, reportTask *types.ReportTask) []error {
	if report.Metadata != nil && report.Metadata.MD5.Valid && u.DropReportExtraRepo.IsDropReportExtraMD5Exist(ctx, report.Metadata.MD5.String) {
		return []error{ErrMD5Conflict}
	}
	return nil
}
