package reportverifs

import (
	"context"
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/repo"
)

// Verifier decides the reliability of the reports of a task. Its zero value, given the repositories, is ready to use,
// by several goroutines at once.
type Verifier struct {
	DropInfoRepo   *repo.DropInfo
	RejectRuleRepo *repo.RejectRule

	// now is the clock of the caches, time.Now unless a test sets it
	now func() time.Time
	// rejectRules holds the active reject rules, read-only, for cacheTTL
	rejectRules expiringCache[struct{}, []*model.RejectRule]
	// dropInfos holds the current drop infos by stage and server (see currentDropInfos)
	dropInfos expiringCache[repo.DropInfoQuery, stageDropInfos]
}

func (v *Verifier) clock() time.Time {
	if v.now != nil {
		return v.now()
	}
	return time.Now()
}

// Reliability checks a report of the task, in this order: its drops against the drop infos of its stage, current now
// (6 if they do not fit), then the active reject rules by rule id (the reliability of the first one it matches). Both
// are as read up to cacheTTL ago, but for the drop infos of a time range that has ended since (see currentDropInfos). A
// report passing both gets 0. The message says why a report is rejected, as the pipeline's Violation did, and is empty
// for 0. A database error is returned: the task is to be retried, not stored with a reliability the checks did not
// decide.
func (v *Verifier) Reliability(ctx context.Context, reportTask *types.ReportTask, report *types.ReportTaskSingleReport) (reliability int, message string, err error) {
	start := time.Now()
	violations, err := v.verifyDrops(ctx, report, reportTask)
	observability.ReportVerifyDuration.WithLabelValues("drop").Observe(time.Since(start).Seconds())
	if err != nil {
		return 0, "", err
	}
	if len(violations) > 0 {
		var b strings.Builder
		for i, err := range violations {
			b.WriteString(err.Error())
			if i < len(violations)-1 {
				b.WriteString(", ")
			}
		}
		// debug: reportwkr.violations logs the rejection of the task at warn level
		log.Debug().
			Str("evt.name", "verifier.drop.rejected").
			Str("taskId", reportTask.TaskID).
			Str("stageId", report.StageID).
			Str("violations", b.String()).
			Msg("drops do not fit the drop infos, rejecting")
		return constant.ViolationReliabilityDrop, b.String(), nil
	}

	start = time.Now()
	reliability, message, err = v.rejectRuleReliability(ctx, report, reportTask)
	observability.ReportVerifyDuration.WithLabelValues("reject_rule").Observe(time.Since(start).Seconds())
	return reliability, message, err
}
