package reportverifs

import (
	"context"
	"fmt"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/antonmedv/expr"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"golang.org/x/mod/semver"

	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/repo"
)

var ErrExprMatched = errors.New("reject expr matched")

type RejectRuleVerifier struct {
	RejectRuleRepo *repo.RejectRule
}

// ensure RejectRuleVerifier conforms to pipelineVerifier
var _ pipelineVerifier = (*RejectRuleVerifier)(nil)

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

		shouldReject := resultHandler(result)

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

// rejectRuleReliability returns the reliability of the first active reject rule the report matches, by rule id, or 0
// if it matches none. A rule whose reliability is out of range, or that fails to evaluate, is skipped.
func (v *Verifier) rejectRuleReliability(ctx context.Context, report *types.ReportTaskSingleReport, reportTask *types.ReportTask) (int, error) {
	rejectRules, err := v.RejectRuleRepo.GetAllActiveRejectRules(ctx)
	if err != nil {
		return 0, errors.Wrap(err, "failed to get reject rules")
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

		shouldReject := resultHandler(result)

		if shouldReject {
			log.Warn().
				Str("evt.name", "verifier.reject_rule.rejected").
				Interface("context", reportContext).
				Int("reject_rule.rule_id", rejectRule.RuleID).
				Int("reject_rule.with_reliability", rejectRule.WithReliability).
				Bool("verifier.evaluation.should_reject", shouldReject).
				Msg("reject rule matched, rejecting using specified reliability value")

			return rejectRule.WithReliability, nil
		} else {
			if l := log.Trace(); l.Enabled() {
				l.Interface("context", reportContext).
					Int("ruleId", rejectRule.RuleID).
					Msgf("reject rule verification passed")
			}
		}
	}

	return 0, nil
}

func resultHandler(result any) bool {
	switch r := result.(type) {
	case bool:
		return r
	default:
		log.Error().Msgf("reject rule expr result type %T is not supported", result)
		return false
	}
}
