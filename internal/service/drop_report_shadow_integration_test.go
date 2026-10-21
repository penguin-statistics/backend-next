package service

// Integration test of shadow mode (#752) on the drop matrix test's reports and cold storage. Skipped unless
// PENGUIN_TEST_POSTGRES_DSN and PENGUIN_TEST_DUCKDB_EXTENSION_DIR are set.

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/coldstore"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo"
)

func TestShadowModeAnswersFromPostgresAndComparesTheSplit(t *testing.T) {
	extDir := os.Getenv("PENGUIN_TEST_DUCKDB_EXTENSION_DIR")
	if extDir == "" {
		t.Skip("PENGUIN_TEST_DUCKDB_EXTENSION_DIR is not set")
	}
	captureLogs(t)
	db := newTestDB(t)
	ctx := context.Background()
	r := rand.New(rand.NewSource(752))
	seedColdTestReports(t, r, db)
	reader := newColdTestStore(t, db, extDir)

	coldRepo, err := repo.NewColdDropReport(&appconfig.Config{ConfigSpec: appconfig.ConfigSpec{
		ColdstoreShadowEnabled: true, ColdstoreShadowTimeout: time.Minute, ColdstoreShadowMaxInFlight: 2,
	}}, reader)
	if err != nil {
		t.Fatal(err)
	}
	pgOnly := NewDropReport(repo.NewDropReport(db), nil)
	shadow := NewDropReport(repo.NewDropReport(db), coldRepo)
	if shadow.shadow == nil {
		t.Fatal("shadow mode is off")
	}
	view := coldRepo.View()
	sourceCategories := []string{constant.SourceCategoryAll, constant.SourceCategoryManual, constant.SourceCategoryAutomated}

	queries := coldTestQueries(r)
	type trendQuery struct {
		trend     coldTestTrend
		server    string
		accountId null.Int
		filter    map[int][]int
	}
	var trends []trendQuery
	for _, trend := range coldTestTrends() {
		for _, mode := range []struct {
			server    string
			accountId null.Int
		}{{"CN", null.Int{}}, {"US", null.Int{}}, {"CN", null.IntFrom(1)}} {
			trends = append(trends, trendQuery{trend, mode.server, mode.accountId, map[int][]int{1: {11, 12}, coldTestRecruitStageID: {11, 12, 13, 14}}})
		}
	}

	// the queries that reach back before the boundary, which shadow mode compares
	want := map[string]float64{}
	for _, q := range queries {
		if cold, _ := splitQueryContexts(view, q.queryCtxs); len(cold.queryCtxs) > 0 {
			want["drop_matrix"]++
		}
		if len(q.queryCtxs) == 1 {
			if cold, _ := splitQueryContexts(view, q.queryCtxs); len(cold.queryCtxs) > 0 {
				want["pattern_matrix"]++
			}
		}
	}
	for _, q := range trends {
		if _, _, ok := splitTrend(view, q.server, q.trend.start, q.trend.intervalLength, q.trend.intervalNum); ok {
			want["trend_quantity"]++
			want["trend_times"]++
		}
	}
	for query, n := range want {
		if n < 10 {
			t.Fatalf("only %v %s queries reach back before the boundary", n, query)
		}
	}

	run := func(stage string) map[string]map[string]float64 {
		t.Helper()
		deltas := map[string]func() map[string]float64{}
		for query := range want {
			deltas[query] = shadowComparisonsDelta(t, query)
		}
		// each comparison is waited for, so that none is skipped
		for _, q := range queries {
			wantTimes, wantQuantities, err := pgOnly.CalcDropMatrixForIntervalsBySourceCategories(ctx, q.queryCtxs, sourceCategories)
			if err != nil {
				t.Fatal(err)
			}
			times, quantities, err := shadow.CalcDropMatrixForIntervalsBySourceCategories(ctx, q.queryCtxs, sourceCategories)
			if err != nil {
				t.Fatalf("%s: %s: %v", stage, q.name, err)
			}
			shadow.shadow.wait()
			for j := range q.queryCtxs {
				for _, sourceCategory := range sourceCategories {
					what := fmt.Sprintf("%s: %s, interval %d, %s", stage, q.name, j, sourceCategory)
					assertSameColdTestCounts(t, what+": times", coldTestTimesByStage(wantTimes[j][sourceCategory]), coldTestTimesByStage(times[j][sourceCategory]))
					assertSameColdTestCounts(t, what+": quantities", coldTestQuantities(wantQuantities[j][sourceCategory]), coldTestQuantities(quantities[j][sourceCategory]))
				}
			}

			if len(q.queryCtxs) != 1 {
				continue
			}
			wantPatterns, err := pgOnly.CalcTotalQuantityForPatternMatrixBySourceCategories(ctx, q.queryCtxs[0], sourceCategories)
			if err != nil {
				t.Fatal(err)
			}
			patterns, err := shadow.CalcTotalQuantityForPatternMatrixBySourceCategories(ctx, q.queryCtxs[0], sourceCategories)
			if err != nil {
				t.Fatalf("%s: %s: %v", stage, q.name, err)
			}
			shadow.shadow.wait()
			for _, sourceCategory := range sourceCategories {
				assertSameColdTestCounts(t, fmt.Sprintf("%s: %s, %s: patterns", stage, q.name, sourceCategory),
					coldTestPatternQuantities(wantPatterns[sourceCategory]), coldTestPatternQuantities(patterns[sourceCategory]))
			}
		}
		for _, q := range trends {
			what := fmt.Sprintf("%s: trend %s, %s, account %v", stage, q.trend.name, q.server, q.accountId.Valid)
			stageIds := []int{1, coldTestRecruitStageID}
			wantQuantities, err := pgOnly.CalcTotalQuantityForTrend(ctx, q.server, &q.trend.start, q.trend.intervalLength, q.trend.intervalNum, q.filter, q.accountId, constant.SourceCategoryAll)
			if err != nil {
				t.Fatal(err)
			}
			wantTimes, err := pgOnly.CalcTotalTimesForTrend(ctx, q.server, &q.trend.start, q.trend.intervalLength, q.trend.intervalNum, stageIds, q.accountId, constant.SourceCategoryAll)
			if err != nil {
				t.Fatal(err)
			}
			quantities, err := shadow.CalcTotalQuantityForTrend(ctx, q.server, &q.trend.start, q.trend.intervalLength, q.trend.intervalNum, q.filter, q.accountId, constant.SourceCategoryAll)
			if err != nil {
				t.Fatalf("%s: %v", what, err)
			}
			shadow.shadow.wait()
			times, err := shadow.CalcTotalTimesForTrend(ctx, q.server, &q.trend.start, q.trend.intervalLength, q.trend.intervalNum, stageIds, q.accountId, constant.SourceCategoryAll)
			if err != nil {
				t.Fatalf("%s: %v", what, err)
			}
			shadow.shadow.wait()
			assertSameLines(t, what, coldTestTrendLines(wantQuantities, wantTimes), coldTestTrendLines(quantities, times))
		}

		counted := map[string]map[string]float64{}
		for query, delta := range deltas {
			counted[query] = delta()
		}
		return counted
	}

	// PostgreSQL still holds every report: the split results match
	for query, counts := range run("all in PostgreSQL") {
		if len(counts) != 1 || counts[coldstore.ShadowMatch] != want[query] {
			t.Errorf("all in PostgreSQL: %s: counted %v, want %v matches", query, counts, want[query])
		}
	}

	// the archived reports deleted from PostgreSQL: the answers are still PostgreSQL's, without them, and the
	// queries over archived reports differ
	mustExec(t, db, "DELETE FROM drop_reports WHERE created_at < ?", coldTestBoundary)
	for query, counts := range run("archived reports deleted from PostgreSQL") {
		if counts[coldstore.ShadowMatch]+counts[coldstore.ShadowMismatch] != want[query] || counts[coldstore.ShadowMismatch] < want[query]/2 {
			t.Errorf("archived reports deleted: %s: counted %v, want %v comparisons, most of them mismatches", query, counts, want[query])
		}
	}
}

// TestShadowModeEndsItsPostgresStatementsOnTheServer blocks the PostgreSQL part of each split computation behind a
// lock on drop_reports: once the shadow timeout is spent, PostgreSQL must have cancelled the statement, not merely
// the driver given up on it while it keeps waiting on the server.
func TestShadowModeEndsItsPostgresStatementsOnTheServer(t *testing.T) {
	extDir := os.Getenv("PENGUIN_TEST_DUCKDB_EXTENSION_DIR")
	if extDir == "" {
		t.Skip("PENGUIN_TEST_DUCKDB_EXTENSION_DIR is not set")
	}
	captureLogs(t)
	db := newTestDB(t)
	ctx := context.Background()
	seedColdTestReports(t, rand.New(rand.NewSource(752)), db)
	reader := newColdTestStore(t, db, extDir)
	const timeout = 1500 * time.Millisecond
	coldRepo, err := repo.NewColdDropReport(&appconfig.Config{ConfigSpec: appconfig.ConfigSpec{
		ColdstoreShadowEnabled: true, ColdstoreShadowTimeout: timeout, ColdstoreShadowMaxInFlight: 1,
	}}, reader)
	if err != nil {
		t.Fatal(err)
	}
	s := NewDropReport(repo.NewDropReport(db), coldRepo)
	view := coldRepo.View()

	// a session that holds drop_reports: every statement that reads it waits on the server
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	var locker int
	if err := tx.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&locker); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "LOCK TABLE drop_reports IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	blocked := func() int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE ? = ANY(pg_blocking_pids(pid))", locker).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	sourceCategories := []string{constant.SourceCategoryAll}
	start, end := coldTestBoundary.AddDate(0, 0, -10), coldTestBoundary.AddDate(0, 0, 10)
	cold, hot := splitQueryContexts(view, []*model.DropReportQueryContext{{Server: "CN", StartTime: &start, EndTime: &end}})
	trendStart, trendLength, trendNum := time.Date(2019, 6, 25, 0, 0, 0, 0, time.UTC), 24*time.Hour, 10
	trendCold, trendHot, ok := splitTrend(view, "CN", trendStart, trendLength, trendNum)
	if len(cold.queryCtxs) != 1 || len(hot.queryCtxs) != 1 || !ok || trendHot.Empty() {
		t.Fatalf("the queries do not cross the boundary: %+v %+v %+v %+v", cold, hot, trendCold, trendHot)
	}
	splits := []struct {
		query string
		split func(ctx context.Context) error
	}{
		{"drop_matrix", func(ctx context.Context) error {
			_, _, err := s.calcDropMatrixSplit(ctx, view, 1, cold, hot, sourceCategories)
			return err
		}},
		{"pattern_matrix", func(ctx context.Context) error {
			_, err := s.calcPatternMatrixSplit(ctx, view, cold, hot, sourceCategories)
			return err
		}},
		{"trend_quantity", func(ctx context.Context) error {
			_, err := s.calcTrendQuantitySplit(ctx, view, trendCold, trendHot, "CN", &trendStart, trendLength, trendNum, map[int][]int{1: {11}}, null.Int{}, constant.SourceCategoryAll)
			return err
		}},
		{"trend_times", func(ctx context.Context) error {
			_, err := s.calcTrendTimesSplit(ctx, view, trendCold, trendHot, "CN", &trendStart, trendLength, trendNum, []int{1}, null.Int{}, constant.SourceCategoryAll)
			return err
		}},
	}
	for _, c := range splits {
		delta := shadowComparisonsDelta(t, c.query)
		started := time.Now()
		if _, err := runShadow(ctx, s.shadow, shadowQuery{query: c.query, server: "CN", start: start, end: end, hotBoundary: view.HotBoundary()},
			func(context.Context) (struct{}, error) { return struct{}{}, nil },
			func(ctx context.Context) (struct{}, error) { return struct{}{}, c.split(ctx) },
			func(struct{}) shadowCounts { return shadowCounts{} }); err != nil {
			t.Fatal(err)
		}
		s.shadow.wait()
		elapsed := time.Since(started)
		if d := delta(); len(d) != 1 || d[coldstore.ShadowTimeout] != 1 {
			t.Errorf("%s: counted %v, want one timeout", c.query, d)
		}
		if elapsed > timeout+time.Second {
			t.Errorf("%s: the computation took %v, beyond its timeout of %v", c.query, elapsed, timeout)
		}
		// nothing of the computation is left on the server
		for deadline := time.Now().Add(time.Second); ; time.Sleep(20 * time.Millisecond) {
			n := blocked()
			if n == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %d statements still wait on the server %v after the computation started (timeout %v)", c.query, n, time.Since(started), timeout)
			}
		}
	}
}

// TestShadowModeCountsACancelledStatementAsAnError checks that a statement of the split cancelled by pg_cancel_backend,
// which has the SQLSTATE of a statement timeout, counts as an error, not as a timeout.
func TestShadowModeCountsACancelledStatementAsAnError(t *testing.T) {
	logs := captureLogs(t)
	db := newTestDB(t)
	ctx := context.Background()
	sh := newColdShadow(repo.ColdShadowSettings{Enabled: true, Timeout: time.Minute, MaxInFlight: 1},
		repo.NewDropReport(db).RunWithStatementTimeout)
	marker := fmt.Sprintf("shadow cancel test %d", time.Now().UnixNano())
	go func() {
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			var cancelled int
			if err := db.NewRaw("SELECT count(pg_cancel_backend(pid)) FROM pg_stat_activity WHERE query LIKE ? AND pid <> pg_backend_pid()", "%"+marker).Scan(ctx, &cancelled); err != nil || cancelled > 0 {
				return
			}
		}
	}()

	delta := shadowComparisonsDelta(t, "test_cancelled")
	if _, err := runShadow(ctx, sh, testShadowQuery("test_cancelled"),
		func(context.Context) (struct{}, error) { return struct{}{}, nil },
		func(ctx context.Context) (struct{}, error) {
			_, err := db.ExecContext(ctx, "SELECT pg_sleep(5) -- "+marker)
			return struct{}{}, err
		},
		func(struct{}) shadowCounts { return shadowCounts{} }); err != nil {
		t.Fatal(err)
	}
	sh.wait()
	if d := delta(); len(d) != 1 || d[coldstore.ShadowError] != 1 {
		t.Errorf("counted %v, want one error", d)
	}
	if !strings.Contains(logs.String(), "coldstore.shadow."+coldstore.ShadowError) {
		t.Errorf("no coldstore.shadow.error log in %s", logs.String())
	}
}
