package reportverifs

import (
	"context"
	"fmt"

	"github.com/antonmedv/expr"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/model/types"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

const RejectRuleUnexpectedViolationReliability = 7

var ErrExprMatched = errors.New("reject expr matched")

type RejectRuleVerifier struct {
	RejectRuleRepo *repo.RejectRule
}

// ensure RejectRuleVerifier conforms to Verifier
var _ Verifier = (*RejectRuleVerifier)(nil)

func NewRejectRuleVerifier(rejectRuleRepo *repo.RejectRule) *RejectRuleVerifier {
	return &RejectRuleVerifier{
		RejectRuleRepo: rejectRuleRepo,
	}
}

func (d *RejectRuleVerifier) Name() string {
	return "reject_rule"
}

type ReportContext struct {
	Report *types.ReportTaskSingleReport
	Task   *types.ReportTask
}

func (d *RejectRuleVerifier) Verify(ctx context.Context, report *types.ReportTaskSingleReport, reportTask *types.ReportTask) *Rejection {
	rejectRules, err := d.RejectRuleRepo.GetAllActiveRejectRules(ctx)
	if err != nil {
		return &Rejection{
			Reliability: RejectRuleUnexpectedViolationReliability,
			Message:     err.Error(),
		}
	}

	reportContext := ReportContext{
		Report: report,
		Task:   reportTask,
	}

	for _, rejectRule := range rejectRules {
		result, err := expr.Eval(rejectRule.Expr, reportContext)
		if err != nil {
			log.Error().
				Interface("context", reportContext).
				Str("expr", rejectRule.Expr).
				Err(err).
				Msgf("failed to evaluate reject rule %d", rejectRule.RuleID)
			continue
		}

		shouldReject := d.resultHandler(result)

		if shouldReject {
			log.Warn().
				Interface("context", reportContext).
				Str("expr", rejectRule.Expr).
				Bool("shouldReject", shouldReject).
				Msgf("reject rule %d matched (rejecting using reliability value %d)", rejectRule.RuleID, rejectRule.WithReliability)

			return &Rejection{
				Reliability: rejectRule.WithReliability,
				Message:     fmt.Sprintf("reject rule %d matched", rejectRule.RuleID),
			}
		} else {
			if l := log.Trace(); l.Enabled() {
				l.Interface("context", reportContext).
					Str("expr", rejectRule.Expr).
					Msgf("reject rule %d verification passed", rejectRule.RuleID)
			}
		}
	}

	return nil
}

func (d *RejectRuleVerifier) resultHandler(result any) bool {
	switch result.(type) {
	case bool:
		return result.(bool)
	default:
		log.Error().Msgf("reject rule expr result type %T is not supported", result)
		return false
	}
}
