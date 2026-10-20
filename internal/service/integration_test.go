package service

// Integration tests comparing the worker's calculations with those of v3.14.18 (legacy_test.go) on a real
// PostgreSQL. They are skipped unless PENGUIN_TEST_POSTGRES_DSN points at a disposable database, see
// internal/repo/integration_test.go. Every test works in a schema of its own, which it drops afterwards.

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/repo"
)

// testSchema is the subset of the production schema the tested calculations read.
const testSchema = `
CREATE TYPE server AS ENUM ('CN', 'US', 'JP', 'KR');
CREATE TYPE drop_type AS ENUM ('REGULAR', 'SPECIAL', 'EXTRA', 'FURNITURE', 'RECOGNITION_ONLY');
CREATE TABLE time_ranges (
	range_id bigserial PRIMARY KEY,
	name character varying(64),
	start_time timestamp with time zone NOT NULL,
	end_time timestamp with time zone NOT NULL,
	comment text,
	server server NOT NULL
);
CREATE TABLE drop_infos (
	drop_id bigserial PRIMARY KEY,
	server server NOT NULL,
	stage_id integer NOT NULL,
	item_id integer,
	drop_type drop_type NOT NULL,
	range_id bigint NOT NULL,
	bounds jsonb,
	accumulable boolean NOT NULL,
	extras jsonb
);
CREATE TABLE drop_reports (
	report_id bigserial PRIMARY KEY,
	stage_id integer NOT NULL,
	pattern_id bigint NOT NULL,
	times integer NOT NULL,
	created_at timestamp with time zone DEFAULT now() NOT NULL,
	reliability integer NOT NULL,
	server server NOT NULL,
	account_id bigint NOT NULL,
	source_name text,
	version text
);
CREATE TABLE drop_pattern_elements (
	element_id bigserial PRIMARY KEY,
	drop_pattern_id bigint NOT NULL,
	item_id integer NOT NULL,
	quantity integer NOT NULL
);
CREATE TABLE drop_matrix_elements (
	element_id bigserial PRIMARY KEY,
	stage_id integer NOT NULL,
	item_id integer NOT NULL,
	quantity integer NOT NULL,
	times integer NOT NULL,
	server server NOT NULL,
	quantity_buckets jsonb,
	source_category character varying(32) DEFAULT 'all' NOT NULL,
	start_time timestamp with time zone,
	end_time timestamp with time zone,
	day_num integer NOT NULL
);
CREATE TABLE stages (
	stage_id serial PRIMARY KEY,
	ark_stage_id character varying(255) NOT NULL,
	zone_id integer NOT NULL,
	stage_type text NOT NULL,
	extra_process_type text,
	code jsonb,
	sanity integer,
	existence jsonb,
	min_clear_time integer
);
CREATE TABLE items (
	item_id serial PRIMARY KEY,
	ark_item_id character varying(255) NOT NULL,
	type text NOT NULL,
	name jsonb,
	existence jsonb,
	sort_id integer NOT NULL DEFAULT 0,
	rarity integer NOT NULL DEFAULT 0,
	"group" text,
	sprite text,
	keywords jsonb
);
`

func newTestDB(t *testing.T) *bun.DB {
	t.Helper()
	dsn := os.Getenv("PENGUIN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PENGUIN_TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("it_%d_%d", time.Now().UnixNano(), rand.Int63())

	admin := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn))), pgdialect.New())
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})

	db := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(
		pgdriver.WithDSN(dsn),
		pgdriver.WithConnParams(map[string]interface{}{"search_path": schema}),
	)), pgdialect.New())
	t.Cleanup(func() { db.Close() })
	if _, err := db.ExecContext(ctx, testSchema); err != nil {
		t.Fatal(err)
	}
	return db
}

func mustExec(t *testing.T, db *bun.DB, query string, args ...interface{}) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

// flushCaches empties every cache, as the calculations under test and their references share them.
func flushCaches(t *testing.T) {
	t.Helper()
	cache.Initialize()
	for _, flush := range cache.SetMap {
		if err := flush(); err != nil {
			t.Fatal(err)
		}
	}
	for _, flush := range cache.SingularFlusherMap {
		if err := flush(); err != nil {
			t.Fatal(err)
		}
	}
}

var (
	testServers  = []string{"CN", "US"}
	testBaseTime = time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
)

// seedMatrixData inserts time ranges (overlapping, adjacent, with equal starts, ending within a day), drop infos of
// several drop types with accumulable and non-accumulable ranges, drop patterns, and reports of several sources,
// reliabilities and accounts over five days.
func seedMatrixData(t *testing.T, r *rand.Rand, db *bun.DB) {
	t.Helper()
	ctx := context.Background()
	for _, server := range testServers {
		var rangeIds []int64
		for i := 0; i < 8; i++ {
			start := testBaseTime.Add(time.Duration(r.Intn(4*24)) * time.Hour)
			if i%3 == 0 && i > 0 { // the same start as an earlier range
				var previous time.Time
				if err := db.NewSelect().TableExpr("time_ranges").Column("start_time").Where("range_id = ?", rangeIds[len(rangeIds)-1]).Scan(ctx, &previous); err != nil {
					t.Fatal(err)
				}
				start = previous
			}
			end := start.Add(time.Duration(6+r.Intn(72)) * time.Hour)
			var rangeId int64
			if err := db.NewRaw("INSERT INTO time_ranges (name, start_time, end_time, server) VALUES (?, ?, ?, ?) RETURNING range_id",
				fmt.Sprintf("%s-%d", server, i), start, end, server).Scan(ctx, &rangeId); err != nil {
				t.Fatal(err)
			}
			rangeIds = append(rangeIds, rangeId)
		}
		for stageId := 1; stageId <= 6; stageId++ {
			for _, rangeId := range rangeIds {
				if r.Intn(2) == 0 {
					continue
				}
				accumulable := r.Intn(4) != 0
				mustExec(t, db, "INSERT INTO drop_infos (server, stage_id, item_id, drop_type, range_id, accumulable) VALUES (?, ?, NULL, 'REGULAR', ?, ?)",
					server, stageId, rangeId, accumulable)
				for itemId := 10; itemId <= 15; itemId++ {
					if r.Intn(2) == 0 {
						continue
					}
					dropType := []string{"REGULAR", "SPECIAL", "EXTRA", "RECOGNITION_ONLY"}[r.Intn(4)]
					mustExec(t, db, "INSERT INTO drop_infos (server, stage_id, item_id, drop_type, range_id, accumulable) VALUES (?, ?, ?, ?, ?, ?)",
						server, stageId, itemId, dropType, rangeId, accumulable)
				}
			}
		}
	}

	for patternId := 2; patternId <= 25; patternId++ {
		for itemId := 10; itemId <= 15; itemId++ {
			if r.Intn(3) == 0 {
				mustExec(t, db, "INSERT INTO drop_pattern_elements (drop_pattern_id, item_id, quantity) VALUES (?, ?, ?)", patternId, itemId, 1+r.Intn(3))
			}
		}
	}
	sources := []interface{}{nil, constant.ManualSources[0], "MeoAssistant"}
	for i := 0; i < 4000; i++ {
		times := 1
		if r.Intn(6) == 0 {
			times = 2 + r.Intn(4)
		}
		reliability := 0
		if r.Intn(8) == 0 {
			reliability = 1
		}
		mustExec(t, db, `INSERT INTO drop_reports (stage_id, pattern_id, times, created_at, reliability, server, account_id, source_name)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			1+r.Intn(6), 1+r.Intn(25), times, testBaseTime.Add(time.Duration(r.Int63n(int64(5*24*time.Hour)))), reliability,
			testServers[r.Intn(len(testServers))], 1+r.Intn(3), sources[r.Intn(len(sources))])
	}
	flushCaches(t)
}

type matrixServices struct {
	timeRange     *TimeRange
	dropMatrix    *DropMatrix
	patternMatrix *PatternMatrix
}

func newMatrixServices(db *bun.DB) matrixServices {
	dropInfoRepo := repo.NewDropInfo(db)
	timeRange := NewTimeRange(repo.NewTimeRange(db), dropInfoRepo)
	dropInfo := NewDropInfo(dropInfoRepo, timeRange)
	dropReport := NewDropReport(repo.NewDropReport(db))
	return matrixServices{
		timeRange:     timeRange,
		dropMatrix:    &DropMatrix{TimeRangeService: timeRange, DropReportService: dropReport, DropInfoService: dropInfo},
		patternMatrix: &PatternMatrix{TimeRangeService: timeRange, DropReportService: dropReport, DropInfoService: dropInfo},
	}
}

func timeRangeLine(tr *model.TimeRange) string {
	return fmt.Sprintf("%d-%d", tr.StartTime.UnixMilli(), tr.EndTime.UnixMilli())
}

func timeRangesByStageAndItemLines(m map[int]map[int][]*model.TimeRange) []string {
	lines := make([]string, 0)
	for stageId, byItem := range m {
		for itemId, timeRanges := range byItem {
			parts := make([]string, 0, len(timeRanges))
			for _, tr := range timeRanges {
				parts = append(parts, timeRangeLine(tr)) // in order: the order is part of the result
			}
			lines = append(lines, fmt.Sprintf("%d|%d|%s", stageId, itemId, strings.Join(parts, ",")))
		}
	}
	sort.Strings(lines)
	return lines
}

func dropMatrixElementLines(elements []*model.DropMatrixElement) []string {
	lines := make([]string, 0, len(elements))
	for _, el := range elements {
		keys := make([]int, 0, len(el.QuantityBuckets))
		for k := range el.QuantityBuckets {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		var b strings.Builder
		for _, k := range keys {
			fmt.Fprintf(&b, "%d:%d,", k, el.QuantityBuckets[k])
		}
		lines = append(lines, fmt.Sprintf("%s|%s|%d|%d|%d|%d|%d|%d|%d|%s", el.Server, el.SourceCategory, el.StageID, el.ItemID,
			el.Quantity, el.Times, el.StartTime.UnixMilli(), el.EndTime.UnixMilli(), el.DayNum, b.String()))
	}
	sort.Strings(lines)
	return lines
}

func patternMatrixElementLines(elements []*model.PatternMatrixElement) []string {
	lines := make([]string, 0, len(elements))
	for _, el := range elements {
		lines = append(lines, fmt.Sprintf("%s|%s|%d|%d|%d|%d|%d|%d|%d|%d", el.Server, el.SourceCategory, el.StageID, el.PatternID,
			el.Quantity, el.Times, el.StartTime.UnixMilli(), el.EndTime.UnixMilli(), el.DayNum, el.RangeID))
	}
	sort.Strings(lines)
	return lines
}

// assertSameLines compares canonical result lines and returns how many were compared.
func assertSameLines(t *testing.T, what string, want, got []string) int {
	t.Helper()
	if strings.Join(want, "\n") != strings.Join(got, "\n") {
		missing, extra := diffLines(want, got)
		t.Fatalf("%s: %d lines, want %d\nmissing: %v\nextra:   %v", what, len(got), len(want), missing, extra)
	}
	return len(want)
}

// requireCompared fails a test whose data left the compared results (nearly) empty.
func requireCompared(t *testing.T, compared, atLeast int) {
	t.Helper()
	if compared < atLeast {
		t.Fatalf("compared %d result lines, want at least %d: the test data does not exercise the calculation", compared, atLeast)
	}
	t.Logf("compared %d result lines", compared)
}

func diffLines(want, got []string) (missing, extra []string) {
	count := make(map[string]int)
	for _, line := range want {
		count[line]++
	}
	for _, line := range got {
		count[line]--
	}
	for line, c := range count {
		for ; c > 0; c-- {
			missing = append(missing, line)
		}
		for ; c < 0; c++ {
			extra = append(extra, line)
		}
	}
	return missing, extra
}

func TestTimeRangesMatchLegacy(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	compared := 0
	for seed := int64(1); seed <= 3; seed++ {
		mustExec(t, db, "TRUNCATE time_ranges, drop_infos, drop_reports, drop_pattern_elements")
		seedMatrixData(t, rand.New(rand.NewSource(seed)), db)
		s := newMatrixServices(db).timeRange
		for _, server := range testServers {
			what := fmt.Sprintf("seed %d %s", seed, server)

			want, err := legacyGetAllMaxAccumulableTimeRangesByServer(ctx, s, server)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.GetAllMaxAccumulableTimeRangesByServer(ctx, server)
			if err != nil {
				t.Fatal(err)
			}
			compared += assertSameLines(t, what+" all max accumulable time ranges", timeRangesByStageAndItemLines(want), timeRangesByStageAndItemLines(got))

			want, err = legacyGetMaxAccumulableTimeRangesByServer(ctx, s, server)
			if err != nil {
				t.Fatal(err)
			}
			got, err = s.GetMaxAccumulableTimeRangesByServer(ctx, server)
			if err != nil {
				t.Fatal(err)
			}
			compared += assertSameLines(t, what+" max accumulable time ranges", timeRangesByStageAndItemLines(want), timeRangesByStageAndItemLines(got))

			wantLatest, err := legacyGetLatestTimeRangesByServer(ctx, s, server)
			if err != nil {
				t.Fatal(err)
			}
			gotLatest, err := s.GetLatestTimeRangesByServer(ctx, server)
			if err != nil {
				t.Fatal(err)
			}
			wantLines, gotLines := make([]string, 0), make([]string, 0)
			for stageId, tr := range wantLatest {
				wantLines = append(wantLines, fmt.Sprintf("%d|%d", stageId, tr.RangeID))
			}
			for stageId, tr := range gotLatest {
				gotLines = append(gotLines, fmt.Sprintf("%d|%d", stageId, tr.RangeID))
			}
			sort.Strings(wantLines)
			sort.Strings(gotLines)
			compared += assertSameLines(t, what+" latest time ranges", wantLines, gotLines)
		}
	}
	requireCompared(t, compared, 50)
}

func TestCalcDropMatrixByGivenDateMatchesLegacy(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	sourceCategories := []string{constant.SourceCategoryAll, constant.SourceCategoryManual, constant.SourceCategoryAutomated}
	compared := 0
	for seed := int64(1); seed <= 3; seed++ {
		mustExec(t, db, "TRUNCATE time_ranges, drop_infos, drop_reports, drop_pattern_elements")
		seedMatrixData(t, rand.New(rand.NewSource(seed)), db)
		s := newMatrixServices(db).dropMatrix
		for _, server := range testServers {
			for day := 0; day < 5; day++ {
				date := testBaseTime.Add(time.Duration(day)*24*time.Hour + 12*time.Hour)
				partialEnd := date.Add(5 * time.Hour)
				for _, endTime := range []*time.Time{nil, &partialEnd} {
					what := fmt.Sprintf("seed %d %s day %d partial %v", seed, server, day, endTime != nil)
					want, err := legacyCalcDropMatrixByGivenDate(ctx, s, server, &date, endTime, sourceCategories)
					if err != nil {
						t.Fatal(err)
					}
					got, err := s.calcDropMatrixByGivenDate(ctx, server, &date, endTime, sourceCategories)
					if err != nil {
						t.Fatal(err)
					}
					compared += assertSameLines(t, what, dropMatrixElementLines(want), dropMatrixElementLines(got))
				}
			}
		}
	}
	requireCompared(t, compared, 200)
}

func TestCalcPatternMatrixForTimeRangesMatchesLegacy(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	sourceCategories := []string{constant.SourceCategoryAll, constant.SourceCategoryManual, constant.SourceCategoryAutomated}
	compared := 0
	for seed := int64(1); seed <= 3; seed++ {
		mustExec(t, db, "TRUNCATE time_ranges, drop_infos, drop_reports, drop_pattern_elements")
		seedMatrixData(t, rand.New(rand.NewSource(seed)), db)
		s := newMatrixServices(db).patternMatrix
		for _, server := range testServers {
			timeRanges, err := s.TimeRangeService.GetTimeRangesByServer(ctx, server)
			if err != nil {
				t.Fatal(err)
			}
			for _, accountId := range []null.Int{{}, null.IntFrom(2)} {
				for _, stageIdFilter := range [][]int{nil, {1, 3, 5}} {
					for i, timeRange := range timeRanges {
						what := fmt.Sprintf("seed %d %s account %v stages %v range %d", seed, server, accountId.Int64, stageIdFilter, i)
						want := make([]*model.PatternMatrixElement, 0)
						for _, sourceCategory := range sourceCategories {
							elements, err := legacyCalcPatternMatrixForTimeRanges(ctx, s, server, []*model.TimeRange{timeRange}, stageIdFilter, accountId, sourceCategory)
							if err != nil {
								t.Fatal(err)
							}
							want = append(want, elements...)
						}
						got, err := s.calcPatternMatrixForTimeRanges(ctx, server, []*model.TimeRange{timeRange}, stageIdFilter, accountId, sourceCategories)
						if err != nil {
							t.Fatal(err)
						}
						compared += assertSameLines(t, what, patternMatrixElementLines(want), patternMatrixElementLines(got))
					}
				}
			}
		}
	}
	requireCompared(t, compared, 200)
}
