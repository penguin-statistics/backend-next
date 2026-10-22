package service

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/coldstore"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo"
)

// shadowMaxLoggedDifferences is how many differences a mismatch logs, the first in key order.
const shadowMaxLoggedDifferences = 10

// shadowOpenEndGrace is how close to the start of a query of shadow mode the end of a part of it (an interval of a
// matrix, a bucket of a trend) may be for a difference in that part to be expected: PostgreSQL alone and the hot part
// of the split read PostgreSQL in two statements, and the reports written in between (dated when they were received,
// written a little later) are counted by one of them only. A mismatch whose differences are all in such parts counts
// as coldstore.ShadowMismatchOpen. Parts that end before are complete in PostgreSQL, and a difference there is real,
// even when another part of the same query reaches to now.
const shadowOpenEndGrace = time.Hour

// shadowStatementTimeout is the statement timeout of the PostgreSQL aggregations of a computation of shadow mode
// whose context ends after timeout: a little shorter, so that PostgreSQL cancels a statement before the driver gives
// up on it (see coldShadow).
func shadowStatementTimeout(timeout time.Duration) time.Duration {
	return timeout - min(timeout/10, time.Second)
}

// coldShadow runs the split computations of shadow mode (ColdstoreShadowEnabled) in the background: the queries are
// answered from PostgreSQL alone, and a split result that differs is logged and counted
// (penguinbackend_coldstore_shadow_comparisons_total). At most cap(slots) computations run at once.
//
// A computation's context ends after timeout, and its PostgreSQL aggregations run under withStatementTimeout
// (repo.DropReport.RunWithStatementTimeout) with shadowStatementTimeout(timeout): pgdriver does not cancel a statement
// on the server when its context ends, so without it a computation that timed out would release its slot and leave
// its statement running on the server, beyond the timeout and the slots.
type coldShadow struct {
	timeout              time.Duration
	withStatementTimeout func(ctx context.Context, timeout time.Duration, fn func(ctx context.Context) error) error
	slots                chan struct{}
	// wg tracks the computations still running, for tests
	wg sync.WaitGroup
}

func newColdShadow(
	settings repo.ColdShadowSettings, withStatementTimeout func(ctx context.Context, timeout time.Duration, fn func(ctx context.Context) error) error,
) *coldShadow {
	return &coldShadow{timeout: settings.Timeout, withStatementTimeout: withStatementTimeout, slots: make(chan struct{}, settings.MaxInFlight)}
}

// wait waits until the computations started so far are done.
func (sh *coldShadow) wait() {
	sh.wg.Wait()
}

// shadowQuery describes a query of shadow mode for the metrics and logs: no account id, no IP.
type shadowQuery struct {
	// query is the metric label: drop_matrix, pattern_matrix, trend_quantity or trend_times
	query            string
	server           string
	start, end       time.Time
	hotBoundary      time.Time
	hasAccount       bool
	intervals        int
	stages           int
	sourceCategories []string
	// partEnd returns the end of a part (shadowKey.part) of the query; nil when the parts end with the query
	partEnd func(part int) time.Time
}

// endOf returns the end of a part of q (see shadowKey.part).
func (q *shadowQuery) endOf(part int) time.Time {
	if q.partEnd == nil {
		return q.end
	}
	return q.partEnd(part)
}

// shadowKey identifies a count of a result: shadowCounts compares results whatever the order of their rows.
type shadowKey struct {
	// part is the interval of a drop matrix query, or the bucket (group id) of a trend
	part           int
	sourceCategory string
	// kind is what is counted: times, quantity_uniq_count, pattern_quantity or quantity
	kind string
	// ids are the stage id, then the item and quantity, or the pattern
	ids [3]int
}

func (k shadowKey) String() string {
	return fmt.Sprintf("%d/%s/%s/%v", k.part, k.sourceCategory, k.kind, k.ids)
}

func (k shadowKey) less(o shadowKey) bool {
	if k.part != o.part {
		return k.part < o.part
	}
	if k.sourceCategory != o.sourceCategory {
		return k.sourceCategory < o.sourceCategory
	}
	if k.kind != o.kind {
		return k.kind < o.kind
	}
	for i := range k.ids {
		if k.ids[i] != o.ids[i] {
			return k.ids[i] < o.ids[i]
		}
	}
	return false
}

// shadowCounts is a result as its counts by key. Rows of the same key add up and zero counts are left out, so two
// results that differ only in the order of their rows or in rows of zero have the same counts.
type shadowCounts map[shadowKey]int

func (c shadowCounts) add(k shadowKey, v int) {
	if v == 0 {
		return
	}
	if c[k] += v; c[k] == 0 {
		delete(c, k)
	}
}

// diffShadowCounts returns the keys whose counts differ between want and got, sorted.
func diffShadowCounts(want, got shadowCounts) []shadowKey {
	var diff []shadowKey
	for k, v := range want {
		if got[k] != v {
			diff = append(diff, k)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			diff = append(diff, k)
		}
	}
	sort.Slice(diff, func(i, j int) bool { return diff[i].less(diff[j]) })
	return diff
}

// shadowOutcome is what the PostgreSQL computation hands the split one to compare with: ok is false when it failed.
type shadowOutcome struct {
	counts shadowCounts
	ok     bool
}

// runShadow answers a query of shadow mode with pg, run with ctx, and computes it again with split in the background,
// with a context that the end of ctx does not cancel and that ends after the shadow timeout. split runs at the same
// time as pg, so the caller waits for pg only. Once both are done, the counts of the two results are compared, and
// the comparison counted and, unless the results match, logged. split must not share anything with the caller that
// the caller may change after runShadow returns. When every slot is busy, the query is answered by pg alone and
// counted as skipped. A mismatch whose differences are all in parts of q that end after shadowOpenEndGrace before the
// call counts as coldstore.ShadowMismatchOpen. split runs under the statement timeout of sh (see coldShadow).
func runShadow[T any](
	ctx context.Context, sh *coldShadow, q shadowQuery,
	pg, split func(ctx context.Context) (T, error), counts func(T) shadowCounts,
) (T, error) {
	select {
	case sh.slots <- struct{}{}:
	default:
		coldstore.CountShadowComparison(q.query, coldstore.ShadowSkipped)
		return pg(ctx)
	}

	openSince := time.Now().Add(-shadowOpenEndGrace)
	shadowCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sh.timeout)
	want := make(chan shadowOutcome, 1)
	sh.wg.Add(1)
	go func() {
		defer sh.wg.Done()
		defer func() { <-sh.slots }()
		defer cancel()

		started := time.Now()
		got, err := func() (got T, err error) {
			defer func() {
				if r := recover(); r != nil {
					err = errors.Errorf("panic: %v", r)
				}
			}()
			err = sh.withStatementTimeout(shadowCtx, shadowStatementTimeout(sh.timeout), func(ctx context.Context) error {
				var err error
				got, err = split(ctx)
				return err
			})
			return got, err
		}()
		coldstore.ObserveShadow(q.query, started, err)
		elapsed := time.Since(started)
		timedOut := errors.Is(shadowCtx.Err(), context.DeadlineExceeded) || repo.IsStatementTimeout(err)

		outcome := <-want
		if !outcome.ok {
			// PostgreSQL failed: the caller has its error, and split may have been cancelled
			coldstore.CountShadowComparison(q.query, coldstore.ShadowSkipped)
			return
		}
		if err != nil {
			result := coldstore.ShadowError
			if timedOut {
				result = coldstore.ShadowTimeout
			}
			coldstore.CountShadowComparison(q.query, result)
			q.log(log.Warn().Err(err).Str("evt.name", "coldstore.shadow."+result), elapsed).
				Msg("coldstore: the split computation of shadow mode failed")
			return
		}
		gotCounts, diff := shadowCounts(nil), []shadowKey(nil)
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = errors.Errorf("panic: %v", r)
				}
			}()
			gotCounts = counts(got)
			diff = diffShadowCounts(outcome.counts, gotCounts)
		}()
		if err != nil {
			coldstore.CountShadowComparison(q.query, coldstore.ShadowError)
			q.log(log.Error().Err(err).Str("evt.name", "coldstore.shadow.error"), elapsed).
				Msg("coldstore: the comparison of shadow mode failed")
			return
		}
		if len(diff) == 0 {
			coldstore.CountShadowComparison(q.query, coldstore.ShadowMatch)
			return
		}
		// the differences in parts that ended before the grace cannot come from reports written meanwhile
		closed := 0
		for _, k := range diff {
			if !q.endOf(k.part).After(openSince) {
				closed++
			}
		}
		result := coldstore.ShadowMismatch
		if closed == 0 {
			result = coldstore.ShadowMismatchOpen
		}
		coldstore.CountShadowComparison(q.query, result)
		var differences []string
		for _, k := range diff[:min(len(diff), shadowMaxLoggedDifferences)] {
			differences = append(differences, fmt.Sprintf("%s: pg %d, split %d", k, outcome.counts[k], gotCounts[k]))
		}
		q.log(log.Error().Str("evt.name", "coldstore.shadow.mismatch"), elapsed).
			Bool("open_end", closed == 0).
			Int("differences", len(diff)).
			Int("closed_differences", closed).
			Int("pg_counts", len(outcome.counts)).
			Int("split_counts", len(gotCounts)).
			Strs("first_differences", differences).
			Msg("coldstore: the split result of shadow mode differs from PostgreSQL alone")
	}()

	// the counts are taken before the caller gets the result, which it may change
	outcome := shadowOutcome{}
	defer func() {
		if !outcome.ok {
			cancel()
		}
		want <- outcome
	}()
	result, err := pg(ctx)
	if err != nil {
		return result, err
	}
	outcome = shadowOutcome{counts: counts(result), ok: true}
	return result, nil
}

// log adds q and the duration of its split computation to ev.
func (q *shadowQuery) log(ev *zerolog.Event, elapsed time.Duration) *zerolog.Event {
	return ev.Str("query", q.query).
		Str("server", q.server).
		Time("start", q.start).
		Time("end", q.end).
		Time("hot_boundary", q.hotBoundary).
		Bool("account", q.hasAccount).
		Int("intervals", q.intervals).
		Int("stages", q.stages).
		Strs("source_categories", q.sourceCategories).
		Dur("split_duration", elapsed)
}

// cloneQueryContexts copies queryCtxs with their times and stage and item filters, for a computation of shadow mode
// that may outlive the caller.
func cloneQueryContexts(queryCtxs []*model.DropReportQueryContext) []*model.DropReportQueryContext {
	clones := make([]*model.DropReportQueryContext, len(queryCtxs))
	for i, queryCtx := range queryCtxs {
		clone := *queryCtx
		if queryCtx.StartTime != nil {
			start := *queryCtx.StartTime
			clone.StartTime = &start
		}
		if queryCtx.EndTime != nil {
			end := *queryCtx.EndTime
			clone.EndTime = &end
		}
		if queryCtx.StageItemFilter != nil {
			filter := cloneStageIdItemIdMap(*queryCtx.StageItemFilter)
			clone.StageItemFilter = &filter
		}
		clones[i] = &clone
	}
	return clones
}

func cloneStageIdItemIdMap(m map[int][]int) map[int][]int {
	if m == nil {
		return nil
	}
	clone := make(map[int][]int, len(m))
	for stageId, itemIds := range m {
		clone[stageId] = slices.Clone(itemIds)
	}
	return clone
}

// newDropMatrixShadowQuery describes queryCtxs, which split at hotBoundary, from the start of the first interval to
// the end of the last. Its parts are the intervals.
func newDropMatrixShadowQuery(query string, hotBoundary time.Time, queryCtxs []*model.DropReportQueryContext, sourceCategories []string) shadowQuery {
	q := shadowQuery{
		query:            query,
		server:           queryCtxs[0].Server,
		hotBoundary:      hotBoundary,
		hasAccount:       queryCtxs[0].AccountID.Valid,
		intervals:        len(queryCtxs),
		sourceCategories: slices.Clone(sourceCategories),
	}
	stages := make(map[int]struct{})
	ends := make([]time.Time, len(queryCtxs))
	for i, queryCtx := range queryCtxs {
		ends[i] = *queryCtx.EndTime
		if i == 0 || queryCtx.StartTime.Before(q.start) {
			q.start = *queryCtx.StartTime
		}
		if i == 0 || queryCtx.EndTime.After(q.end) {
			q.end = *queryCtx.EndTime
		}
		for _, stageId := range queryCtx.GetStageIds() {
			stages[stageId] = struct{}{}
		}
	}
	q.stages = len(stages)
	q.partEnd = func(part int) time.Time {
		if part < 0 || part >= len(ends) {
			// the counts of intervals (see dropMatrixShadowCounts) differ whenever the queries ran
			return time.Time{}
		}
		return ends[part]
	}
	return q
}

// newTrendShadowQuery describes a trend whose buckets cover cold and hot, split at hotBoundary. Its parts are the
// buckets (group ids).
func newTrendShadowQuery(
	query string, hotBoundary time.Time, server string, startTime time.Time, intervalLength time.Duration, intervalNum int,
	cold, hot coldstore.TimeRange, hasAccount bool, stages int, sourceCategory string,
) shadowQuery {
	q := shadowQuery{
		query:            query,
		server:           server,
		start:            cold.Start,
		end:              cold.End,
		hotBoundary:      hotBoundary,
		hasAccount:       hasAccount,
		intervals:        intervalNum + 1,
		stages:           stages,
		sourceCategories: []string{sourceCategory},
	}
	if !hot.Empty() {
		q.end = hot.End
	}
	bucketStart, _ := repo.TrendTimeRange(server, startTime, intervalLength, intervalNum)
	length := time.Duration(int(intervalLength.Hours())) * time.Hour
	q.partEnd = func(groupId int) time.Time {
		return bucketStart.Add(length * time.Duration(groupId+1))
	}
	return q
}

// dropMatrixResults are the results of CalcDropMatrixForIntervalsBySourceCategories.
type dropMatrixResults struct {
	times      []map[string][]*model.TotalTimesResult
	quantities []map[string][]*model.QuantityUniqCountResultForDropMatrix
}

func dropMatrixShadowCounts(r dropMatrixResults) shadowCounts {
	c := make(shadowCounts)
	for i, byCategory := range r.times {
		for sourceCategory, results := range byCategory {
			for _, result := range results {
				c.add(shadowKey{part: i, sourceCategory: sourceCategory, kind: "times", ids: [3]int{result.StageID}}, result.TotalTimes)
			}
		}
	}
	for i, byCategory := range r.quantities {
		for sourceCategory, results := range byCategory {
			for _, result := range results {
				c.add(shadowKey{part: i, sourceCategory: sourceCategory, kind: "quantity_uniq_count", ids: [3]int{result.StageID, result.ItemID, result.Quantity}}, result.Count)
			}
		}
	}
	// an interval in one result and not in the other differs even without counts
	c.add(shadowKey{part: -1, kind: "intervals"}, len(r.times))
	c.add(shadowKey{part: -1, kind: "intervals_quantity"}, len(r.quantities))
	return c
}

func patternMatrixShadowCounts(r map[string][]*model.TotalQuantityResultForPatternMatrix) shadowCounts {
	c := make(shadowCounts)
	for sourceCategory, results := range r {
		for _, result := range results {
			c.add(shadowKey{sourceCategory: sourceCategory, kind: "pattern_quantity", ids: [3]int{result.StageID, result.PatternID}}, result.TotalQuantity)
		}
	}
	return c
}

func trendQuantityShadowCounts(r []*model.TotalQuantityResultForTrend) shadowCounts {
	c := make(shadowCounts)
	for _, result := range r {
		c.add(shadowKey{part: result.GroupID, kind: "quantity", ids: [3]int{result.StageID, result.ItemID}}, result.TotalQuantity)
	}
	return c
}

func trendTimesShadowCounts(r []*model.TotalTimesResultForTrend) shadowCounts {
	c := make(shadowCounts)
	for _, result := range r {
		c.add(shadowKey{part: result.GroupID, kind: "times", ids: [3]int{result.StageID}}, result.TotalTimes)
	}
	return c
}
