package reportverifs

import (
	"context"
	"fmt"
	"sync"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/antonmedv/expr"
	"github.com/antonmedv/expr/vm"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"golang.org/x/mod/semver"

	"exusiai.dev/backend-next/internal/model/types"
)

var ErrExprMatched = errors.New("reject expr matched")

// programCacheMaxEntries bounds programCache: it is cleared once it holds more. An edited rule has a new text, and so
// a new entry.
const programCacheMaxEntries = 256

type compiledProgram struct {
	prog *vm.Program
	err  error
}

var (
	programCacheMu sync.RWMutex
	// programCache holds the program, or the compile error, of each reject rule expression by its text
	programCache = map[string]compiledProgram{}
)

// compiledRule returns the program of a reject rule expression, compiled once as expr.Eval compiles it (with no
// options). A compile error is returned again on every call, as expr.Eval would.
func compiledRule(text string) (*vm.Program, error) {
	programCacheMu.RLock()
	c, ok := programCache[text]
	programCacheMu.RUnlock()
	if ok {
		return c.prog, c.err
	}

	prog, err := expr.Compile(text)
	programCacheMu.Lock()
	if len(programCache) >= programCacheMaxEntries {
		programCache = map[string]compiledProgram{}
	}
	programCache[text] = compiledProgram{prog: prog, err: err}
	programCacheMu.Unlock()
	return prog, err
}

type ReportContext struct {
	Report *types.ReportTaskSingleReport
	Task   *types.ReportTask
}

func (ReportContext) SemVerCompare(a, b string) int {
	return semver.Compare(a, b)
}

// rejectRuleReliability returns the reliability of the first active reject rule the report matches, by rule id, with
// the message "reject rule <id> matched", or 0 if it matches none. A rule whose reliability is out of range, or that fails to evaluate, is skipped.
func (v *Verifier) rejectRuleReliability(ctx context.Context, report *types.ReportTaskSingleReport, reportTask *types.ReportTask) (int, string, error) {
	rejectRules, err := v.RejectRuleRepo.GetAllActiveRejectRules(ctx)
	if err != nil {
		return 0, "", errors.Wrap(err, "failed to get reject rules")
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

		var result any
		prog, err := compiledRule(rejectRule.Expr)
		if err == nil {
			result, err = expr.Run(prog, reportContext)
		}
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

			return rejectRule.WithReliability, fmt.Sprintf("reject rule %d matched", rejectRule.RuleID), nil
		} else {
			if l := log.Trace(); l.Enabled() {
				l.Interface("context", reportContext).
					Int("ruleId", rejectRule.RuleID).
					Msgf("reject rule verification passed")
			}
		}
	}

	return 0, "", nil
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
