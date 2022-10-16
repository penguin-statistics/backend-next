package reportverifs

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/penguin-statistics/backend-next/internal/model/types"
	"github.com/penguin-statistics/backend-next/internal/pkg/observability"
)

var tracer = otel.Tracer("reportverifs")

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

			ctx, span := tracer.
				Start(ctx, "reportverifs.verifier."+name)

			rejection := pipe.Verify(ctx, report, reportTask)
			span.End()

			observability.ReportVerifyDuration.
				WithLabelValues(name).
				Observe(time.Since(start).Seconds())

			if rejection != nil {
				violations[reportIndex] = &Violation{
					Name:      name,
					Rejection: *rejection,
				}

				break
			}
		}
	}

	return violations
}
