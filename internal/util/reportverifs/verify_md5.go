package reportverifs

import (
	"context"

	"github.com/pkg/errors"

	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/model/types"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

var ErrMD5Conflict = errors.New("report with specified md5 has already existed")

type MD5Verifier struct {
	DropReportExtraRepo *repo.DropReportExtra
}

// ensure MD5Verifier conforms to Verifier
var _ Verifier = (*MD5Verifier)(nil)

func NewMD5Verifier(dropReportExtraRepo *repo.DropReportExtra) *MD5Verifier {
	return &MD5Verifier{
		DropReportExtraRepo: dropReportExtraRepo,
	}
}

func (u *MD5Verifier) Name() string {
	return "md5"
}

func (u *MD5Verifier) Verify(ctx context.Context, report *types.ReportTaskSingleReport, reportTask *types.ReportTask) *Rejection {
	if report.Metadata != nil && report.Metadata.MD5 != "" && u.DropReportExtraRepo.IsDropReportExtraMD5Exist(ctx, report.Metadata.MD5) {
		return &Rejection{
			Reliability: constant.ViolationReliabilityMD5,
			Message:     ErrMD5Conflict.Error(),
		}
	}
	return nil
}
