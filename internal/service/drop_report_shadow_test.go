package service

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/coldstore"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo"
)

// shadowComparisons returns penguinbackend_coldstore_shadow_comparisons_total{query, result}.
func shadowComparisons(t *testing.T, query, result string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "penguinbackend_coldstore_shadow_comparisons_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["query"] == query && labels["result"] == result {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// shadowComparisonsDelta returns a function that returns how much each result of query was counted since.
func shadowComparisonsDelta(t *testing.T, query string) func() map[string]float64 {
	t.Helper()
	results := []string{coldstore.ShadowMatch, coldstore.ShadowMismatch, coldstore.ShadowMismatchOpen, coldstore.ShadowError,
		coldstore.ShadowTimeout, coldstore.ShadowSkipped}
	before := map[string]float64{}
	for _, result := range results {
		before[result] = shadowComparisons(t, query, result)
	}
	return func() map[string]float64 {
		t.Helper()
		delta := map[string]float64{}
		for _, result := range results {
			if d := shadowComparisons(t, query, result) - before[result]; d != 0 {
				delta[result] = d
			}
		}
		return delta
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLogs sends the global logger to a buffer for the test.
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	logger := log.Logger
	log.Logger = zerolog.New(buf)
	t.Cleanup(func() { log.Logger = logger })
	return buf
}

func testShadowQuery(query string) shadowQuery {
	return shadowQuery{
		query: query, server: "CN",
		start:       time.Date(2019, 5, 1, 0, 0, 0, 0, time.UTC),
		end:         time.Date(2019, 8, 1, 0, 0, 0, 0, time.UTC),
		hotBoundary: time.Date(2019, 7, 1, 0, 0, 0, 0, time.UTC),
		hasAccount:  true, intervals: 1, stages: 2, sourceCategories: []string{"all"},
	}
}

func testShadow(maxInFlight int, timeout time.Duration) *coldShadow {
	return newColdShadow(repo.ColdShadowSettings{Enabled: true, Timeout: timeout, MaxInFlight: maxInFlight},
		func(ctx context.Context, _ time.Duration, fn func(ctx context.Context) error) error { return fn(ctx) })
}

func timesRows(rows ...[2]int) []*model.TotalTimesResultForTrend {
	results := make([]*model.TotalTimesResultForTrend, 0, len(rows))
	for _, row := range rows {
		results = append(results, &model.TotalTimesResultForTrend{GroupID: 0, StageID: row[0], TotalTimes: row[1]})
	}
	return results
}

func TestRunShadowReturnsPostgresAndCountsTheComparison(t *testing.T) {
	logs := captureLogs(t)
	ctx := context.Background()

	t.Run("match whatever the order", func(t *testing.T) {
		delta := shadowComparisonsDelta(t, "test_match")
		sh := testShadow(2, time.Minute)
		got, err := runShadow(ctx, sh, testShadowQuery("test_match"),
			func(context.Context) ([]*model.TotalTimesResultForTrend, error) {
				return timesRows([2]int{1, 5}, [2]int{2, 3}), nil
			},
			func(context.Context) ([]*model.TotalTimesResultForTrend, error) {
				// in another order, with a row of zero and a stage in two rows
				return timesRows([2]int{2, 1}, [2]int{3, 0}, [2]int{1, 5}, [2]int{2, 2}), nil
			},
			trendTimesShadowCounts)
		if err != nil || len(got) != 2 || got[0].StageID != 1 {
			t.Fatalf("got %v, %v, want the PostgreSQL result", got, err)
		}
		sh.wait()
		if d := delta(); len(d) != 1 || d[coldstore.ShadowMatch] != 1 {
			t.Errorf("counted %v, want one match", d)
		}
	})

	t.Run("mismatch", func(t *testing.T) {
		delta := shadowComparisonsDelta(t, "test_mismatch")
		sh := testShadow(2, time.Minute)
		got, err := runShadow(ctx, sh, testShadowQuery("test_mismatch"),
			func(context.Context) ([]*model.TotalTimesResultForTrend, error) {
				return timesRows([2]int{1, 5}, [2]int{2, 3}), nil
			},
			func(context.Context) ([]*model.TotalTimesResultForTrend, error) {
				return timesRows([2]int{1, 4}, [2]int{4, 1}), nil
			},
			trendTimesShadowCounts)
		if err != nil || len(got) != 2 || got[0].TotalTimes != 5 {
			t.Fatalf("got %v, %v, want the PostgreSQL result", got, err)
		}
		sh.wait()
		if d := delta(); len(d) != 1 || d[coldstore.ShadowMismatch] != 1 {
			t.Errorf("counted %v, want one mismatch", d)
		}
		line := ""
		for _, l := range strings.Split(logs.String(), "\n") {
			if strings.Contains(l, `"query":"test_mismatch"`) {
				line = l
			}
		}
		for _, want := range []string{
			`"evt.name":"coldstore.shadow.mismatch"`, `"server":"CN"`, `"account":true`, `"start":"2019-05-01T00:00:00Z"`,
			`"end":"2019-08-01T00:00:00Z"`, `"hot_boundary":"2019-07-01T00:00:00Z"`, `"open_end":false`, `"differences":3`,
			`"0//times/[1 0 0]: pg 5, split 4","0//times/[2 0 0]: pg 3, split 0","0//times/[4 0 0]: pg 0, split 1"`,
		} {
			if !strings.Contains(line, want) {
				t.Errorf("mismatch log %q lacks %s", line, want)
			}
		}
	})

	t.Run("mismatch of a range that reaches to now", func(t *testing.T) {
		// PostgreSQL may have written reports between its two reads: counted apart from the real mismatches
		delta := shadowComparisonsDelta(t, "test_mismatch_open")
		sh := testShadow(2, time.Minute)
		q := testShadowQuery("test_mismatch_open")
		q.end = time.Now().Add(-shadowOpenEndGrace / 2)
		_, err := runShadow(ctx, sh, q,
			func(context.Context) ([]*model.TotalTimesResultForTrend, error) { return timesRows([2]int{1, 6}), nil },
			func(context.Context) ([]*model.TotalTimesResultForTrend, error) { return timesRows([2]int{1, 5}), nil },
			trendTimesShadowCounts)
		if err != nil {
			t.Fatal(err)
		}
		sh.wait()
		if d := delta(); len(d) != 1 || d[coldstore.ShadowMismatchOpen] != 1 {
			t.Errorf("counted %v, want one mismatch_open", d)
		}
		if !strings.Contains(logs.String(), `"open_end":true`) {
			t.Error("the mismatch log lacks open_end")
		}
	})

	t.Run("split error and panic", func(t *testing.T) {
		delta := shadowComparisonsDelta(t, "test_error")
		sh := testShadow(2, time.Minute)
		for _, split := range []func(context.Context) ([]*model.TotalTimesResultForTrend, error){
			func(context.Context) ([]*model.TotalTimesResultForTrend, error) {
				return nil, errors.New("duckdb failed")
			},
			func(context.Context) ([]*model.TotalTimesResultForTrend, error) { panic("boom") },
		} {
			got, err := runShadow(ctx, sh, testShadowQuery("test_error"),
				func(context.Context) ([]*model.TotalTimesResultForTrend, error) { return timesRows([2]int{1, 5}), nil },
				split, trendTimesShadowCounts)
			if err != nil || len(got) != 1 {
				t.Fatalf("got %v, %v, want the PostgreSQL result", got, err)
			}
		}
		sh.wait()
		if d := delta(); len(d) != 1 || d[coldstore.ShadowError] != 2 {
			t.Errorf("counted %v, want two errors", d)
		}
	})

	t.Run("postgres error", func(t *testing.T) {
		delta := shadowComparisonsDelta(t, "test_pg_error")
		sh := testShadow(2, time.Minute)
		splitCtxDone := make(chan error, 1)
		_, err := runShadow(ctx, sh, testShadowQuery("test_pg_error"),
			func(context.Context) ([]*model.TotalTimesResultForTrend, error) { return nil, errors.New("pg failed") },
			func(ctx context.Context) ([]*model.TotalTimesResultForTrend, error) {
				<-ctx.Done()
				splitCtxDone <- ctx.Err()
				return nil, ctx.Err()
			},
			trendTimesShadowCounts)
		if err == nil || err.Error() != "pg failed" {
			t.Fatalf("got %v, want the PostgreSQL error", err)
		}
		sh.wait()
		// the split computation is cancelled, not left to run into the timeout
		if err := <-splitCtxDone; !errors.Is(err, context.Canceled) {
			t.Errorf("split context ended with %v, want cancelled", err)
		}
		if d := delta(); len(d) != 1 || d[coldstore.ShadowSkipped] != 1 {
			t.Errorf("counted %v, want one skipped", d)
		}
	})
}

func TestRunShadowDoesNotWaitForTheSplitComputation(t *testing.T) {
	captureLogs(t)
	delta := shadowComparisonsDelta(t, "test_slow")
	sh := testShadow(1, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	splitErr := make(chan error, 1)

	started := time.Now()
	pgResult := timesRows([2]int{1, 5})
	got, err := runShadow(ctx, sh, testShadowQuery("test_slow"),
		func(context.Context) ([]*model.TotalTimesResultForTrend, error) { return pgResult, nil },
		func(ctx context.Context) ([]*model.TotalTimesResultForTrend, error) {
			<-release
			splitErr <- ctx.Err()
			return timesRows([2]int{1, 5}), nil
		},
		trendTimesShadowCounts)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("runShadow waited %v for the split computation", elapsed)
	}
	// the caller is done: its context ends and it changes its result, neither of which the comparison sees
	cancel()
	got[0].TotalTimes = 99

	// the only slot is busy: another query is not compared
	if _, err := runShadow(context.Background(), sh, testShadowQuery("test_slow"),
		func(context.Context) ([]*model.TotalTimesResultForTrend, error) { return nil, nil },
		func(context.Context) ([]*model.TotalTimesResultForTrend, error) {
			t.Error("a split computation ran beyond the slots")
			return nil, nil
		},
		trendTimesShadowCounts); err != nil {
		t.Fatal(err)
	}

	close(release)
	sh.wait()
	if err := <-splitErr; err != nil {
		t.Errorf("the end of the caller's context ended the split computation's: %v", err)
	}
	if d := delta(); len(d) != 2 || d[coldstore.ShadowMatch] != 1 || d[coldstore.ShadowSkipped] != 1 {
		t.Errorf("counted %v, want one match and one skipped", d)
	}
}

func TestRunShadowTimeout(t *testing.T) {
	logs := captureLogs(t)
	delta := shadowComparisonsDelta(t, "test_timeout")
	sh := testShadow(1, 50*time.Millisecond)
	if _, err := runShadow(context.Background(), sh, testShadowQuery("test_timeout"),
		func(context.Context) ([]*model.TotalTimesResultForTrend, error) { return nil, nil },
		func(ctx context.Context) ([]*model.TotalTimesResultForTrend, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		trendTimesShadowCounts); err != nil {
		t.Fatal(err)
	}
	sh.wait()
	if d := delta(); len(d) != 1 || d[coldstore.ShadowTimeout] != 1 {
		t.Errorf("counted %v, want one timeout", d)
	}
	if !strings.Contains(logs.String(), `"evt.name":"coldstore.shadow.timeout"`) {
		t.Errorf("no timeout logged: %s", logs.String())
	}
	// the slot is free again
	if len(sh.slots) != 0 {
		t.Errorf("%d slots still taken", len(sh.slots))
	}
}

func TestRunShadowBoundsPostgresOnTheServer(t *testing.T) {
	captureLogs(t)
	// the split runs within the statement timeout of the repository, which ends before its context
	type statementTimeoutKey struct{}
	var gotTimeout, untilDeadline time.Duration
	sh := newColdShadow(repo.ColdShadowSettings{Enabled: true, Timeout: 30 * time.Second, MaxInFlight: 1},
		func(ctx context.Context, timeout time.Duration, fn func(ctx context.Context) error) error {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Error("the split's context has no deadline")
			}
			gotTimeout, untilDeadline = timeout, time.Until(deadline)
			return fn(context.WithValue(ctx, statementTimeoutKey{}, timeout))
		})
	splitTimeout := make(chan any, 1)
	if _, err := runShadow(context.Background(), sh, testShadowQuery("test_statement_timeout"),
		func(context.Context) ([]*model.TotalTimesResultForTrend, error) { return nil, nil },
		func(ctx context.Context) ([]*model.TotalTimesResultForTrend, error) {
			splitTimeout <- ctx.Value(statementTimeoutKey{})
			return nil, nil
		},
		trendTimesShadowCounts); err != nil {
		t.Fatal(err)
	}
	sh.wait()
	if got := <-splitTimeout; got != gotTimeout {
		t.Errorf("the split ran with statement timeout %v, not within the repository's %v", got, gotTimeout)
	}
	if gotTimeout != 29*time.Second || gotTimeout >= untilDeadline {
		t.Errorf("statement timeout %v with %v left until the context's deadline, want 29s, before it", gotTimeout, untilDeadline)
	}
	for timeout, want := range map[time.Duration]time.Duration{time.Minute: 59 * time.Second, 2 * time.Second: 1800 * time.Millisecond} {
		if got := shadowStatementTimeout(timeout); got != want {
			t.Errorf("shadowStatementTimeout(%v) = %v, want %v", timeout, got, want)
		}
	}
}

// TestRunShadowClassifiesEachPart checks that a difference in a part of a query (an interval, a bucket) that ended
// long ago is a mismatch, even when another part reaches to now, and that only differences in the parts that reach
// to now are mismatch_open.
func TestRunShadowClassifiesEachPart(t *testing.T) {
	logs := captureLogs(t)
	ctx := context.Background()
	now := time.Now()
	boundary := time.Date(2019, 7, 1, 0, 0, 0, 0, time.UTC)
	classify := func(t *testing.T, query string, q shadowQuery, pg, split func(context.Context) (dropMatrixResults, error)) map[string]float64 {
		t.Helper()
		delta := shadowComparisonsDelta(t, query)
		sh := testShadow(1, time.Minute)
		if _, err := runShadow(ctx, sh, q, pg, split, dropMatrixShadowCounts); err != nil {
			t.Fatal(err)
		}
		sh.wait()
		return delta()
	}

	// a personal matrix: a closed day of 2019 and the latest range, up to now
	historicalStart, historicalEnd := time.Date(2019, 5, 3, 0, 0, 0, 0, time.UTC), time.Date(2019, 5, 4, 0, 0, 0, 0, time.UTC)
	currentStart := now.Add(-24 * time.Hour)
	queryCtxs := []*model.DropReportQueryContext{
		{Server: "CN", StartTime: &historicalStart, EndTime: &historicalEnd, AccountID: null.IntFrom(1)},
		{Server: "CN", StartTime: &currentStart, EndTime: &now, AccountID: null.IntFrom(1)},
	}
	matrix := func(historical, current int) func(context.Context) (dropMatrixResults, error) {
		return func(context.Context) (dropMatrixResults, error) {
			return dropMatrixResults{
				times: []map[string][]*model.TotalTimesResult{
					{"all": {{StageID: 1, TotalTimes: historical}}},
					{"all": {{StageID: 1, TotalTimes: current}}},
				},
				quantities: []map[string][]*model.QuantityUniqCountResultForDropMatrix{{"all": {}}, {"all": {}}},
			}, nil
		}
	}
	for _, c := range []struct {
		name      string
		split     func(context.Context) (dropMatrixResults, error)
		want      string
		openEnd   string
		closedLog string
	}{
		{"matrix, difference in the closed interval", matrix(4, 7), coldstore.ShadowMismatch, `"open_end":false`, `"closed_differences":1`},
		{"matrix, differences in both", matrix(4, 6), coldstore.ShadowMismatch, `"open_end":false`, `"closed_differences":1`},
		{"matrix, difference in the current interval", matrix(5, 6), coldstore.ShadowMismatchOpen, `"open_end":true`, `"closed_differences":0`},
	} {
		t.Run(c.name, func(t *testing.T) {
			query := "test_parts_" + strings.ReplaceAll(c.name, " ", "_")
			q := newDropMatrixShadowQuery(query, boundary, queryCtxs, []string{"all"})
			if d := classify(t, query, q, matrix(5, 7), c.split); len(d) != 1 || d[c.want] != 1 {
				t.Errorf("counted %v, want one %s", d, c.want)
			}
			line := ""
			for _, l := range strings.Split(logs.String(), "\n") {
				if strings.Contains(l, `"query":"`+query+`"`) {
					line = l
				}
			}
			if !strings.Contains(line, c.openEnd) || !strings.Contains(line, c.closedLog) {
				t.Errorf("mismatch log %q lacks %s or %s", line, c.openEnd, c.closedLog)
			}
		})
	}

	// a trend of 30-day buckets from 2019-05 to now: its first buckets ended years ago, its last one is open
	trendStart := time.Date(2019, 5, 1, 0, 0, 0, 0, time.UTC)
	length := 30 * 24 * time.Hour
	start, _ := repo.TrendTimeRange("CN", trendStart, length, 0)
	intervalNum := int(now.Sub(start) / length)
	_, end := repo.TrendTimeRange("CN", trendStart, length, intervalNum)
	if !end.After(now) || end.Add(-length).After(now) {
		t.Fatalf("the last bucket [%v, %v) does not hold now", end.Add(-length), end)
	}
	q := newTrendShadowQuery("test_trend_parts", boundary, "CN", trendStart, length, intervalNum,
		coldstore.TimeRange{Start: start, End: boundary}, coldstore.TimeRange{Start: boundary, End: end}, false, 1, "all")
	trend := func(first, last int) func(context.Context) ([]*model.TotalTimesResultForTrend, error) {
		return func(context.Context) ([]*model.TotalTimesResultForTrend, error) {
			return []*model.TotalTimesResultForTrend{
				{GroupID: 0, StageID: 1, TotalTimes: first},
				{GroupID: intervalNum, StageID: 1, TotalTimes: last},
			}, nil
		}
	}
	for _, c := range []struct {
		name  string
		split func(context.Context) ([]*model.TotalTimesResultForTrend, error)
		want  string
	}{
		{"historical bucket", trend(4, 7), coldstore.ShadowMismatch},
		{"current bucket", trend(5, 6), coldstore.ShadowMismatchOpen},
	} {
		t.Run("trend, difference in the "+c.name, func(t *testing.T) {
			q := q
			q.query = "test_trend_parts_" + strings.ReplaceAll(c.name, " ", "_")
			delta := shadowComparisonsDelta(t, q.query)
			sh := testShadow(1, time.Minute)
			if _, err := runShadow(ctx, sh, q, trend(5, 7), c.split, trendTimesShadowCounts); err != nil {
				t.Fatal(err)
			}
			sh.wait()
			if d := delta(); len(d) != 1 || d[c.want] != 1 {
				t.Errorf("counted %v, want one %s", d, c.want)
			}
		})
	}
}

func TestShadowCounts(t *testing.T) {
	a := dropMatrixResults{
		times: []map[string][]*model.TotalTimesResult{
			{"all": {{StageID: 1, TotalTimes: 3}, {StageID: 2, TotalTimes: 4}}, "manual": {}},
			{"all": {}},
		},
		quantities: []map[string][]*model.QuantityUniqCountResultForDropMatrix{
			{"all": {{StageID: 1, ItemID: 11, Quantity: 1, Count: 2}, {StageID: 1, ItemID: 11, Quantity: 2, Count: 1}}},
			{"all": {}},
		},
	}
	b := dropMatrixResults{
		times: []map[string][]*model.TotalTimesResult{
			{"all": {{StageID: 2, TotalTimes: 4}, {StageID: 1, TotalTimes: 3}, {StageID: 5, TotalTimes: 0}}},
			{},
		},
		quantities: []map[string][]*model.QuantityUniqCountResultForDropMatrix{
			{"all": {{StageID: 1, ItemID: 11, Quantity: 2, Count: 1}, {StageID: 1, ItemID: 11, Quantity: 1, Count: 2}}},
			nil,
		},
	}
	if diff := diffShadowCounts(dropMatrixShadowCounts(a), dropMatrixShadowCounts(b)); len(diff) != 0 {
		t.Errorf("the same drop matrix in another order differs: %v", diff)
	}
	// a count moved to another interval, a quantity changed, an interval missing
	b.times[1]["all"] = []*model.TotalTimesResult{{StageID: 1, TotalTimes: 3}}
	b.times[0]["all"] = b.times[0]["all"][:2]
	b.times[0]["all"][1] = &model.TotalTimesResult{StageID: 1, TotalTimes: 0}
	b.quantities[0]["all"][0] = &model.QuantityUniqCountResultForDropMatrix{StageID: 1, ItemID: 11, Quantity: 3, Count: 1}
	b.quantities = b.quantities[:1]
	diff := diffShadowCounts(dropMatrixShadowCounts(a), dropMatrixShadowCounts(b))
	var got []string
	for _, k := range diff {
		got = append(got, k.String())
	}
	want := "-1//intervals_quantity/[0 0 0] 0/all/quantity_uniq_count/[1 11 2] 0/all/quantity_uniq_count/[1 11 3] 0/all/times/[1 0 0] 1/all/times/[1 0 0]"
	if strings.Join(got, " ") != want {
		t.Errorf("differences\n%s\nwant\n%s", strings.Join(got, " "), want)
	}

	pattern := map[string][]*model.TotalQuantityResultForPatternMatrix{"all": {{StageID: 1, PatternID: 2, TotalQuantity: 3}, {StageID: 1, PatternID: 4, TotalQuantity: 1}}}
	patternReordered := map[string][]*model.TotalQuantityResultForPatternMatrix{"all": {{StageID: 1, PatternID: 4, TotalQuantity: 1}, {StageID: 1, PatternID: 2, TotalQuantity: 3}}, "manual": {}}
	if diff := diffShadowCounts(patternMatrixShadowCounts(pattern), patternMatrixShadowCounts(patternReordered)); len(diff) != 0 {
		t.Errorf("the same pattern matrix in another order differs: %v", diff)
	}
	patternReordered["manual"] = pattern["all"][:1]
	if diff := diffShadowCounts(patternMatrixShadowCounts(pattern), patternMatrixShadowCounts(patternReordered)); len(diff) != 1 {
		t.Errorf("differences %v, want the manual pattern", diff)
	}

	quantities := []*model.TotalQuantityResultForTrend{{GroupID: 0, StageID: 1, ItemID: 11, TotalQuantity: 2}, {GroupID: 1, StageID: 1, ItemID: 11, TotalQuantity: 3}}
	swapped := []*model.TotalQuantityResultForTrend{{GroupID: 1, StageID: 1, ItemID: 11, TotalQuantity: 2}, {GroupID: 0, StageID: 1, ItemID: 11, TotalQuantity: 3}}
	if diff := diffShadowCounts(trendQuantityShadowCounts(quantities), trendQuantityShadowCounts(swapped)); len(diff) != 2 {
		t.Errorf("differences %v, want both buckets", diff)
	}
	if diff := diffShadowCounts(trendQuantityShadowCounts(quantities), trendQuantityShadowCounts([]*model.TotalQuantityResultForTrend{quantities[1], quantities[0]})); len(diff) != 0 {
		t.Errorf("the same trend in another order differs: %v", diff)
	}
}

func TestCloneQueryContexts(t *testing.T) {
	start, end := time.Date(2019, 5, 1, 0, 0, 0, 0, time.UTC), time.Date(2019, 8, 1, 0, 0, 0, 0, time.UTC)
	filter := map[int][]int{1: {11, 12}, 2: {}}
	queryCtxs := []*model.DropReportQueryContext{
		{Server: "CN", StartTime: &start, EndTime: &end, AccountID: null.IntFrom(3), StageItemFilter: &filter, ExcludeNonOneTimes: true, Times: null.IntFrom(1)},
		{Server: "CN", StartTime: &start, EndTime: &end},
	}
	clones := cloneQueryContexts(queryCtxs)
	start, end = start.Add(time.Hour), end.Add(time.Hour)
	filter[1][0] = 99
	filter[3] = nil
	c := clones[0]
	if c.StartTime.Hour() != 0 || c.EndTime.Hour() != 0 || len(*c.StageItemFilter) != 2 || (*c.StageItemFilter)[1][0] != 11 {
		t.Errorf("the clone shares the caller's times or filter: %v %v %v", c.StartTime, c.EndTime, *c.StageItemFilter)
	}
	if c.Server != "CN" || c.AccountID.Int64 != 3 || !c.ExcludeNonOneTimes || c.Times.Int64 != 1 || clones[1].StageItemFilter != nil {
		t.Errorf("the clone lost fields: %+v %+v", c, clones[1])
	}
}
