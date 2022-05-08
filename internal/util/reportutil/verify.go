package reportutil

import (
	"context"

	"github.com/penguin-statistics/backend-next/internal/model/types"
)

type VerifyViolation struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

type Verifier interface {
	Name() string
	Verify(ctx context.Context, report *types.ReportTaskSingleReport, reportTask *types.ReportTask) []error
}

type ReportVerifier []Verifier

func NewReportVerifier(userVerifier *UserVerifier, dropVerifier *DropVerifier, md5Verifier *MD5Verifier) *ReportVerifier {
	return &ReportVerifier{
		userVerifier,
		md5Verifier,
		dropVerifier,
	}
}

func (verifier ReportVerifier) Verify(ctx context.Context, reportTask *types.ReportTask) []*VerifyViolation {
	errs := make([]*VerifyViolation, 0, len(verifier))
	for _, report := range reportTask.Reports {
		for _, pipe := range verifier {
			if innerErrs := pipe.Verify(ctx, report, reportTask); len(innerErrs) > 0 {
				for _, err := range innerErrs {
					errs = append(errs, &VerifyViolation{
						Name:  pipe.Name(),
						Error: err.Error(),
					})
				}
			}
		}
	}
	return errs
}
