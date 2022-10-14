package reportverifs

import (
	"context"
	"fmt"
	"time"

	"github.com/antonmedv/expr"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"golang.org/x/mod/semver"

	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/model/types"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

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

func (ReportContext) SemVerCompare(a, b string) int {
	return semver.Compare(a, b)
}

func (d *RejectRuleVerifier) Verify(ctx context.Context, report *types.ReportTaskSingleReport, reportTask *types.ReportTask) *Rejection {
	rejectRules, err := d.RejectRuleRepo.GetAllActiveRejectRules(ctx)
	if err != nil {
		return &Rejection{
			Reliability: constant.ViolationReliabilityRejectRuleUnexpected,
			Message:     err.Error(),
		}
	}

	reportContext := ReportContext{
		Report: report,
		Task:   reportTask,
	}

	start := time.Now()
	defer func() {
		if l := log.Trace(); l.Enabled() {
			l.Dur("duration", time.Since(start)).
				Msg("reject rule(s) evaluated")
		}
	}()

	for _, rejectRule := range rejectRules {
		if rejectRule.WithReliability < constant.ViolationReliabilityRejectRuleRangeLeast ||
			rejectRule.WithReliability >= constant.ViolationReliabilityRejectRuleRangeMost {
			log.Error().
				Int("ruleId", rejectRule.RuleID).
				Msgf("reject rule with reliability %d is out of range [%d, %d)", rejectRule.WithReliability, constant.ViolationReliabilityRejectRuleRangeLeast, constant.ViolationReliabilityRejectRuleRangeMost)

			continue
		}

		result, err := expr.Eval(rejectRule.Expr, reportContext)
		if err != nil {
			log.Error().
				Str("evt.name", "verifier.reject_rule.expr_eval_error").
				Interface("context", reportContext).
				Int("ruleId", rejectRule.RuleID).
				Err(err).
				Msgf("failed to evaluate reject rule %d", rejectRule.RuleID)
			continue
		}

		shouldReject := d.resultHandler(result)

		if shouldReject {
			log.Warn().
				Str("evt.name", "verifier.reject_rule.rejected").
				Interface("context", reportContext).
				Int("reject_rule.rule_id", rejectRule.RuleID).
				Int("reject_rule.with_reliability", rejectRule.WithReliability).
				Bool("verifier.evaluation.should_reject", shouldReject).
				Msg("reject rule matched, rejecting using specified reliability value")

			return &Rejection{
				Reliability: rejectRule.WithReliability,
				Message:     fmt.Sprintf("reject rule %d matched", rejectRule.RuleID),
			}
		} else {
			if l := log.Trace(); l.Enabled() {
				l.Interface("context", reportContext).
					Int("ruleId", rejectRule.RuleID).
					Msgf("reject rule verification passed")
			}
		}
	}

	return nil
}

func (d *RejectRuleVerifier) resultHandler(result any) bool {
	switch r := result.(type) {
	case bool:
		return r
	default:
		log.Error().Msgf("reject rule expr result type %T is not supported", result)
		return false
	}
}
