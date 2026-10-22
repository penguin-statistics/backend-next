package service

// Integration test of the drop matrix split at the hot boundary of the cold storage (#752): the same reports in
// PostgreSQL and, exported with the archiver's own code, in the cold storage. Skipped unless
// PENGUIN_TEST_POSTGRES_DSN and PENGUIN_TEST_DUCKDB_EXTENSION_DIR are set (see internal/coldstore's
// export_integration_test.go).

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/coldstore"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo"
)

const coldTestRecruitStageID = 42

// the hot boundary of the test's cold storage, which holds pre, 2019-05 and 2019-06
var coldTestBoundary = time.Date(2019, 7, 1, 0, 0, 0, 0, time.UTC)

// coldTestTimes are the times the test's reports cluster around and its intervals start and end at: month and
// hour boundaries, and times within an hour (some with milliseconds, which PostgreSQL's comparison drops).
var coldTestTimes = []time.Time{
	time.Date(2019, 4, 20, 7, 20, 0, 0, time.UTC),
	time.Date(2019, 5, 1, 0, 0, 0, 0, time.UTC),
	time.Date(2019, 5, 1, 2, 0, 0, 0, time.UTC),
	time.Date(2019, 5, 3, 10, 0, 0, 0, time.UTC),
	time.Date(2019, 5, 3, 10, 17, 23, 456e6, time.UTC),
	time.Date(2019, 5, 20, 18, 0, 0, 0, time.UTC),
	time.Date(2019, 6, 1, 0, 0, 0, 0, time.UTC),
	time.Date(2019, 6, 2, 5, 42, 10, 789e6, time.UTC),
	time.Date(2019, 6, 10, 10, 5, 0, 0, time.UTC),
	time.Date(2019, 6, 10, 10, 55, 0, 0, time.UTC),
	time.Date(2019, 6, 28, 13, 30, 15, 0, time.UTC),
	coldTestBoundary,
	time.Date(2019, 7, 2, 3, 30, 0, 0, time.UTC),
	time.Date(2019, 7, 2, 5, 0, 0, 0, time.UTC),
	time.Date(2019, 7, 15, 0, 0, 0, 0, time.UTC),
}

// seedColdTestReports inserts random patterns and reports of several servers, accounts, sources, reliabilities,
// times and stages (the recruit stage among them) from 2019-04-20 to 2019-07-20, many of them just around
// coldTestTimes, with their extras.
func seedColdTestReports(t *testing.T, r *rand.Rand, db *bun.DB) {
	t.Helper()
	mustExec(t, db, `CREATE TABLE drop_report_extras (
		report_id bigint PRIMARY KEY, ip cidr NOT NULL, metadata jsonb, md5 character varying(32))`)
	mustExec(t, db, "INSERT INTO stages (stage_id, ark_stage_id, zone_id, stage_type) VALUES (?, ?, 1, 'NORMAL')",
		coldTestRecruitStageID, constant.RecruitStageID)
	// pattern 1 is the empty pattern: it has no elements
	for patternId := 2; patternId <= 20; patternId++ {
		for itemId := 1; itemId <= 1+r.Intn(4); itemId++ {
			if r.Intn(3) != 0 {
				mustExec(t, db, "INSERT INTO drop_pattern_elements (drop_pattern_id, item_id, quantity) VALUES (?, ?, ?)",
					patternId, 10+itemId, 1+r.Intn(3))
			}
		}
	}

	from, to := time.Date(2019, 4, 20, 0, 0, 0, 0, time.UTC), time.Date(2019, 7, 20, 0, 0, 0, 0, time.UTC)
	stageIds := []int{1, 2, 3, 4, 5, coldTestRecruitStageID}
	sources := []string{"NULL", "'" + constant.ManualSources[0] + "'", "'" + constant.ManualSources[len(constant.ManualSources)-1] + "'", "'MeoAssistant'", "'arknights-auto'"}
	servers := []string{"CN", "CN", "CN", "US"}
	var values []string
	for i := 0; i < 6000; i++ {
		var createdAt time.Time
		switch r.Intn(3) {
		case 0: // anywhere
			createdAt = from.Add(time.Duration(r.Int63n(int64(to.Sub(from)))))
		default: // around one of coldTestTimes, within a second or within an hour
			around := coldTestTimes[r.Intn(len(coldTestTimes))]
			if r.Intn(2) == 0 {
				createdAt = around.Add(time.Duration(r.Intn(9)-4) * 250 * time.Millisecond)
			} else {
				createdAt = around.Add(time.Duration(r.Int63n(int64(2*time.Hour))) - time.Hour)
			}
		}
		reliability := 0
		if r.Intn(6) == 0 {
			reliability = []int{-1, 1, 2}[r.Intn(3)]
		}
		times := 1
		if r.Intn(5) == 0 {
			times = 2 + r.Intn(5)
		}
		values = append(values, fmt.Sprintf("(%d, %d, %d, '%s', %d, '%s', %d, %s)",
			stageIds[r.Intn(len(stageIds))], 1+r.Intn(20), times, createdAt.Format(time.RFC3339Nano), reliability,
			servers[r.Intn(len(servers))], 1+r.Intn(3), sources[r.Intn(len(sources))]))
	}
	for len(values) > 0 {
		n := min(len(values), 500)
		mustExec(t, db, `INSERT INTO drop_reports (stage_id, pattern_id, times, created_at, reliability, server, account_id, source_name)
			VALUES `+strings.Join(values[:n], ", "))
		values = values[n:]
	}
	mustExec(t, db, "INSERT INTO drop_report_extras (report_id, ip, md5) SELECT report_id, '10.0.0.1', md5(report_id::text) FROM drop_reports")
}

// newColdTestStore exports pre, 2019-05 and 2019-06 from db (search_path schema) to a cold storage directory with
// the archiver's code, and opens it.
func newColdTestStore(t *testing.T, db *bun.DB, extDir string) *coldstore.Reader {
	t.Helper()
	ctx := context.Background()
	var schema string
	if err := db.QueryRowContext(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	dsn := os.Getenv("PENGUIN_TEST_POSTGRES_DSN")
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	opts := coldstore.Options{
		PostgresDSN: dsn + sep + "search_path=" + schema, ExtensionDirectory: extDir,
		WorkDir: t.TempDir(), Threads: 2, MemoryLimit: "1GB",
	}
	dir := t.TempDir()
	m := coldstore.NewManifest()
	for _, key := range []string{coldstore.PreMonth, "2019-05", "2019-06"} {
		month, err := coldstore.ParseMonth(key)
		if err != nil {
			t.Fatal(err)
		}
		e, err := coldstore.ExportMonth(ctx, opts, month, dir)
		if err != nil {
			t.Fatal(err)
		}
		if e.RecruitStageID != coldTestRecruitStageID {
			t.Fatalf("recruit stage %d", e.RecruitStageID)
		}
		m.PutMonth(*e)
	}
	dims, err := coldstore.ExportDims(ctx, opts, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.PutDim(*dims)
	if err := coldstore.WriteManifest(filepath.Join(dir, coldstore.ManifestFile), m); err != nil {
		t.Fatal(err)
	}
	r, err := coldstore.OpenReader(coldstore.ReaderConfig{Dir: dir, Threads: 2, MemoryLimit: "256MB", TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if !r.View().HotBoundary().Equal(coldTestBoundary) {
		t.Fatalf("hot boundary %s", r.View().HotBoundary())
	}
	return r
}

// coldTestQuery is a call of CalcDropMatrixForIntervalsBySourceCategories.
type coldTestQuery struct {
	name      string
	queryCtxs []*model.DropReportQueryContext
}

// coldTestQueries returns the queries the test compares: single intervals fully before, fully after, across the
// hot boundary and within an hour, and sets of intervals as the personal drop matrix has, with and without an
// account, stage and item filters (the recruit stage among them) and the times filter.
func coldTestQueries(r *rand.Rand) []coldTestQuery {
	at := func(i int) time.Time { return coldTestTimes[i] }
	single := []struct {
		name       string
		start, end time.Time
	}{
		{"cold whole hours", at(3), at(5)},
		{"cold partial hours", at(4), at(7)},
		{"cold within an hour", at(8), at(9)},
		{"cold from before the epoch", at(0), at(2)},
		{"cold from before any report", time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC), at(6)},
		{"cold up to the boundary", at(10), coldTestBoundary},
		{"hot", at(12), at(14)},
		{"hot from the boundary", coldTestBoundary, at(13)},
		{"across the boundary", at(10), at(13)},
		{"everything", time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2019, 8, 1, 0, 0, 0, 0, time.UTC)},
		{"empty", at(4), at(4)},
	}
	filters := []*map[int][]int{
		nil,
		{1: {}, 2: {}},
		{coldTestRecruitStageID: {}},
		{3: {}, coldTestRecruitStageID: {}},
		{1: {11, 12}, 4: {}},
	}
	type mode struct {
		name               string
		server             string
		accountId          null.Int
		excludeNonOneTimes bool
	}
	modes := []mode{
		{"CN", "CN", null.Int{}, false},
		{"US", "US", null.Int{}, false},
		{"CN one-time", "CN", null.Int{}, true},
		{"CN account 1", "CN", null.IntFrom(1), false},
		{"US account 2", "US", null.IntFrom(2), false},
		{"CN account 3 one-time", "CN", null.IntFrom(3), true},
	}
	newCtx := func(m mode, start, end time.Time, filter *map[int][]int) *model.DropReportQueryContext {
		return &model.DropReportQueryContext{
			Server: m.server, StartTime: &start, EndTime: &end, AccountID: m.accountId,
			StageItemFilter: filter, ExcludeNonOneTimes: m.excludeNonOneTimes,
		}
	}

	var queries []coldTestQuery
	for _, m := range modes {
		for fi, filter := range filters {
			for _, s := range single {
				queries = append(queries, coldTestQuery{
					name:      fmt.Sprintf("%s, filter %d, %s", m.name, fi, s.name),
					queryCtxs: []*model.DropReportQueryContext{newCtx(m, s.start, s.end, filter)},
				})
			}
		}
		// overlapping, nested, adjacent and duplicated intervals with their own filters
		for round := 0; round < 4; round++ {
			var queryCtxs []*model.DropReportQueryContext
			for i := 0; i < 3+r.Intn(10); i++ {
				from := r.Intn(len(coldTestTimes) - 1)
				to := from + 1 + r.Intn(len(coldTestTimes)-1-from)
				queryCtxs = append(queryCtxs, newCtx(m, at(from), at(to), filters[r.Intn(len(filters))]))
			}
			queries = append(queries, coldTestQuery{name: fmt.Sprintf("%s, intervals %d", m.name, round), queryCtxs: queryCtxs})
		}
	}
	return queries
}

func TestDropMatrixSplitAtTheHotBoundaryMatchesPostgres(t *testing.T) {
	extDir := os.Getenv("PENGUIN_TEST_DUCKDB_EXTENSION_DIR")
	if extDir == "" {
		t.Skip("PENGUIN_TEST_DUCKDB_EXTENSION_DIR is not set")
	}
	db := newTestDB(t)
	ctx := context.Background()
	r := rand.New(rand.NewSource(752))
	seedColdTestReports(t, r, db)
	reader := newColdTestStore(t, db, extDir)

	coldRepo, err := repo.NewColdDropReport(&appconfig.Config{ConfigSpec: appconfig.ConfigSpec{ColdstoreQueriesEnabled: true}}, reader)
	if err != nil {
		t.Fatal(err)
	}
	pgOnly := NewDropReport(repo.NewDropReport(db), nil)
	split := NewDropReport(repo.NewDropReport(db), coldRepo)
	sourceCategories := []string{constant.SourceCategoryAll, constant.SourceCategoryManual, constant.SourceCategoryAutomated}

	queries := coldTestQueries(r)
	type result struct {
		times      []map[string][]*model.TotalTimesResult
		quantities []map[string][]*model.QuantityUniqCountResultForDropMatrix
	}
	want := make([]result, len(queries))
	intervals, nonEmpty := 0, 0
	for i, q := range queries {
		times, quantities, err := pgOnly.CalcDropMatrixForIntervalsBySourceCategories(ctx, q.queryCtxs, sourceCategories)
		if err != nil {
			t.Fatal(err)
		}
		want[i] = result{times, quantities}
		intervals += len(q.queryCtxs)
		for _, byCategory := range times {
			if len(byCategory[constant.SourceCategoryAll]) > 0 {
				nonEmpty++
			}
		}
	}
	// the data is dense enough that most intervals count reports
	if nonEmpty < intervals*3/4 {
		t.Fatalf("only %d of %d intervals count reports", nonEmpty, intervals)
	}

	compare := func(stage string) {
		t.Helper()
		for i, q := range queries {
			times, quantities, err := split.CalcDropMatrixForIntervalsBySourceCategories(ctx, q.queryCtxs, sourceCategories)
			if err != nil {
				t.Fatalf("%s: %s: %v", stage, q.name, err)
			}
			if len(times) != len(q.queryCtxs) || len(quantities) != len(q.queryCtxs) {
				t.Fatalf("%s: %s: %d/%d results for %d intervals", stage, q.name, len(times), len(quantities), len(q.queryCtxs))
			}
			for j := range q.queryCtxs {
				for _, sourceCategory := range sourceCategories {
					what := fmt.Sprintf("%s: %s, interval %d, %s", stage, q.name, j, sourceCategory)
					assertSameColdTestCounts(t, what+": times", coldTestTimesByStage(want[i].times[j][sourceCategory]), coldTestTimesByStage(times[j][sourceCategory]))
					assertSameColdTestCounts(t, what+": quantities", coldTestQuantities(want[i].quantities[j][sourceCategory]), coldTestQuantities(quantities[j][sourceCategory]))
				}
			}
		}
	}
	// shadow mode: PostgreSQL still holds every report, and only the part from the boundary on is read from it
	compare("all in PostgreSQL")
	// after the migration: the archived reports are only in the cold storage
	mustExec(t, db, "DELETE FROM drop_reports WHERE created_at < ?", coldTestBoundary)
	compare("archived reports deleted from PostgreSQL")

	// the cold storage alone refuses an interval past its hot boundary
	start, end := coldTestTimes[10], coldTestTimes[13]
	if _, _, err := coldRepo.CalcDropMatrixForIntervalsBySourceCategories(ctx, coldRepo.View(),
		[]*model.DropReportQueryContext{{Server: "CN", StartTime: &start, EndTime: &end}}, sourceCategories); err == nil {
		t.Error("an interval past the hot boundary was read from the cold storage")
	}
}

func assertSameColdTestCounts[K comparable](t *testing.T, what string, want, got map[K]int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d results, want %d\ngot  %v\nwant %v", what, len(got), len(want), got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: %v = %d, want %d", what, k, got[k], v)
		}
	}
}

// the result helpers fail on duplicate keys by keeping the count of an extra row negative
func coldTestTimesByStage(results []*model.TotalTimesResult) map[int]int {
	m := make(map[int]int)
	for _, result := range results {
		if _, ok := m[result.StageID]; ok {
			m[result.StageID] = -1
			continue
		}
		m[result.StageID] = result.TotalTimes
	}
	return m
}

func coldTestQuantities(results []*model.QuantityUniqCountResultForDropMatrix) map[[3]int]int {
	m := make(map[[3]int]int)
	for _, result := range results {
		k := [3]int{result.StageID, result.ItemID, result.Quantity}
		if _, ok := m[k]; ok {
			m[k] = -1
			continue
		}
		m[k] = result.Count
	}
	return m
}
