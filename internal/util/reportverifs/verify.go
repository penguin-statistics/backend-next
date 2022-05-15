package reportverifs

import (
	"context"

	"github.com/penguin-statistics/backend-next/internal/model/types"
)

type Violation struct {
	Rejection
	Name string `json:"name"`
}

type Rejection struct {
	Reliability int    `json:"reliability"`
	Message     string `json:"message"`
}

type Verifier interface {
	Name() string
	Verify(ctx context.Context, report *types.ReportTaskSingleReport, reportTask *types.ReportTask) *Rejection
}

type ReportVerifier []Verifier

func NewReportVerifier(userVerifier *UserVerifier, dropVerifier *DropVerifier, md5Verifier *MD5Verifier, rejectRuleVerifier *RejectRuleVerifier) *ReportVerifier {
	return &ReportVerifier{
		userVerifier,
		md5Verifier,
		dropVerifier,
		rejectRuleVerifier,
	}
}

func (verifier ReportVerifier) Verify(ctx context.Context, reportTask *types.ReportTask) *Violation {
	for _, report := range reportTask.Reports {
		for _, pipe := range verifier {
			rejection := pipe.Verify(ctx, report, reportTask)

			if rejection != nil {
				return &Violation{
					Name:      pipe.Name(),
					Rejection: *rejection,
				}
			}
		}
	}

	return nil
}
