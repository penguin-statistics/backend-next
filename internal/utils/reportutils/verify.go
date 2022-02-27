package reportutils

import (
	"context"

	"github.com/penguin-statistics/backend-next/internal/models/types"
)

type Verifier interface {
	Verify(ctx context.Context, report *types.SingleReport, reportTask *types.ReportTask) error
}

type ReportVerifier []Verifier

func NewReportVerifier(userVerifier *UserVerifier, dropVerifier *DropVerifier, md5Verifier *MD5Verifier) *ReportVerifier {
	return &ReportVerifier{
		userVerifier,
		md5Verifier,
		dropVerifier,
	}
}

func (verifier ReportVerifier) Verify(ctx context.Context, reportTask *types.ReportTask) error {
	for _, report := range reportTask.Reports {
		for _, pipe := range verifier {
			if err := pipe.Verify(ctx, report, reportTask); err != nil {
				return err
			}
		}
	}
	return nil
}
