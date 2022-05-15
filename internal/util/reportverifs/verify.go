package reportverifs

import (
	"context"
	"time"

	"github.com/penguin-statistics/backend-next/internal/model/types"
	"github.com/penguin-statistics/backend-next/internal/pkg/observability"
)

type Verifier interface {
	Name() string
	Verify(ctx context.Context, report *types.ReportTaskSingleReport, reportTask *types.ReportTask) *Rejection
}

type ReportVerifiers []Verifier

func NewReportVerifier(userVerifier *UserVerifier, dropVerifier *DropVerifier, md5Verifier *MD5Verifier, rejectRuleVerifier *RejectRuleVerifier) *ReportVerifiers {
	return &ReportVerifiers{
		userVerifier,
		md5Verifier,
		dropVerifier,
		rejectRuleVerifier,
	}
}

func (verifiers ReportVerifiers) Verify(ctx context.Context, reportTask *types.ReportTask) (violations Violations) {
	violations = map[int]*Violation{}

	for reportIndex, report := range reportTask.Reports {
		for _, pipe := range verifiers {
			start := time.Now()

			name := pipe.Name()
			rejection := pipe.Verify(ctx, report, reportTask)

			if rejection != nil {
				violations[reportIndex] = &Violation{
					Name:      name,
					Rejection: *rejection,
				}

				break
			}

			observability.ReportVerifyDuration.
				WithLabelValues(name).
				Observe(time.Since(start).Seconds())
		}
	}

	return violations
}
