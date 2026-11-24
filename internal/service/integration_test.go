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
CREATE TABLE pattern_matrix_elements (
	element_id bigserial PRIMARY KEY,
	stage_id integer NOT NULL,
	pattern_id bigint NOT NULL,
	quantity integer NOT NULL,
	times integer NOT NULL,
	server server NOT NULL,
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
	// the day versions of the matrix elements, as in production
	migration, err := os.ReadFile("../../scripts/migrations/20261025_matrix_element_day_versions.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(migration)); err != nil {
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
	dropReport := NewDropReport(repo.NewDropReport(db), nil)
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
	compared, personalCompared := 0, 0
	for seed := int64(1); seed <= 3; seed++ {
		mustExec(t, db, "TRUNCATE time_ranges, drop_infos, drop_reports, drop_pattern_elements")
		seedMatrixData(t, rand.New(rand.NewSource(seed)), db)
		s := newMatrixServices(db).timeRange
		for _, server := range testServers {
			what := fmt.Sprintf("seed %d %s", seed, server)

			// #794 changed the segments of the global drop matrix (v3.14.18 merged a time range that is not
			// accumulable with the accumulable ones after it, and split segments at gaps): they are checked against
			// accumulableSegmentsInSQL instead of v3.14.18
			wantAllLines, wantMaxLines := accumulableSegmentsInSQL(t, db, server)
			got, err := s.GetAllMaxAccumulableTimeRangesByServer(ctx, server)
			if err != nil {
				t.Fatal(err)
			}
			compared += assertSameLines(t, what+" all max accumulable time ranges", wantAllLines, timeRangesByStageAndItemLines(got))

			got, err = s.GetMaxAccumulableTimeRangesByServer(ctx, server)
			if err != nil {
				t.Fatal(err)
			}
			compared += assertSameLines(t, what+" max accumulable time ranges (SQL)", wantMaxLines, timeRangesByStageAndItemLines(got))

			// those of the personal drop matrices did not change, but for time ranges of a stage and item starting at
			// the same time, which v3.14.18 ordered as sort.Sort happened to leave them (the production data has none)
			// and which are now ordered by range id
			want, err := legacyGetMaxAccumulableTimeRangesByServer(ctx, s, server)
			if err != nil {
				t.Fatal(err)
			}
			equalStarts := stageItemsWithEqualStartTimes(t, db, server)
			personalCompared += assertSameLines(t, what+" max accumulable time ranges",
				withoutStageItems(timeRangesByStageAndItemLines(want), equalStarts), withoutStageItems(timeRangesByStageAndItemLines(got), equalStarts))

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
	requireCompared(t, personalCompared, 30)
}

// accumulableSegmentsInSQL calculates the segments of accumulableSegments (#794) in SQL, as a reference written
// independently of it: the time ranges of the drop infos of a stage and item, once each, ordered by start time and
// range id, where one starts a segment when it is the first, when it is not accumulable or when the one before it
// is not. It returns the lines of GetAllMaxAccumulableTimeRangesByServer (every segment, from its start to its
// latest end) and of GetMaxAccumulableTimeRangesByServer (the time ranges of the last segment, newest first).
func accumulableSegmentsInSQL(t *testing.T, db *bun.DB, server string) (allLines, maxLines []string) {
	t.Helper()
	var rows []struct {
		StageID      int       `bun:"stage_id"`
		ItemID       int       `bun:"item_id"`
		Segment      int       `bun:"segment"`
		LastSegment  int       `bun:"last_segment"`
		StartTime    time.Time `bun:"start_time"`
		EndTime      time.Time `bun:"end_time"`
		SegmentStart time.Time `bun:"segment_start"`
		SegmentEnd   time.Time `bun:"segment_end"`
	}
	err := db.NewRaw(`WITH ranges AS (
			SELECT DISTINCT ON (di.stage_id, di.item_id, di.range_id) di.stage_id, di.item_id, di.range_id, di.accumulable,
				tr.start_time, tr.end_time
			FROM drop_infos AS di JOIN time_ranges AS tr ON tr.range_id = di.range_id
			WHERE di.server = ? AND di.item_id IS NOT NULL
		), starts AS (
			SELECT *, (NOT accumulable OR NOT COALESCE(lag(accumulable) OVER w, false)) AS starts_segment
			FROM ranges
			WINDOW w AS (PARTITION BY stage_id, item_id ORDER BY start_time, range_id)
		), segments AS (
			SELECT *, count(*) FILTER (WHERE starts_segment) OVER w AS segment
			FROM starts
			WINDOW w AS (PARTITION BY stage_id, item_id ORDER BY start_time, range_id)
		)
		SELECT stage_id, item_id, segment, max(segment) OVER (PARTITION BY stage_id, item_id) AS last_segment,
			start_time, end_time,
			min(start_time) OVER (PARTITION BY stage_id, item_id, segment) AS segment_start,
			max(end_time) OVER (PARTITION BY stage_id, item_id, segment) AS segment_end
		FROM segments
		ORDER BY stage_id, item_id, start_time DESC, range_id DESC`, server).Scan(context.Background(), &rows)
	if err != nil {
		t.Fatal(err)
	}

	type key struct{ stageId, itemId int }
	segmentsByKey := make(map[key][]string)
	latestByKey := make(map[key][]string)
	previousSegment := make(map[key]int)
	keys := make([]key, 0)
	for _, row := range rows {
		k := key{row.StageID, row.ItemID}
		if _, ok := segmentsByKey[k]; !ok {
			keys = append(keys, k)
		}
		// rows are newest first: a segment is prepended at its newest time range
		if previousSegment[k] != row.Segment {
			previousSegment[k] = row.Segment
			segment := fmt.Sprintf("%d-%d", row.SegmentStart.UnixMilli(), row.SegmentEnd.UnixMilli())
			segmentsByKey[k] = append([]string{segment}, segmentsByKey[k]...)
		}
		if row.Segment == row.LastSegment {
			latestByKey[k] = append(latestByKey[k], fmt.Sprintf("%d-%d", row.StartTime.UnixMilli(), row.EndTime.UnixMilli()))
		}
	}
	for _, k := range keys {
		allLines = append(allLines, fmt.Sprintf("%d|%d|%s", k.stageId, k.itemId, strings.Join(segmentsByKey[k], ",")))
		maxLines = append(maxLines, fmt.Sprintf("%d|%d|%s", k.stageId, k.itemId, strings.Join(latestByKey[k], ",")))
	}
	sort.Strings(allLines)
	sort.Strings(maxLines)
	return allLines, maxLines
}

// stageItemsWithEqualStartTimes returns the "stage|item|" prefixes of the lines of the stages and items of server
// with two time ranges starting at the same time.
func stageItemsWithEqualStartTimes(t *testing.T, db *bun.DB, server string) map[string]bool {
	t.Helper()
	var rows []struct {
		StageID int `bun:"stage_id"`
		ItemID  int `bun:"item_id"`
	}
	err := db.NewRaw(`SELECT DISTINCT di.stage_id, di.item_id
		FROM drop_infos AS di JOIN time_ranges AS tr ON tr.range_id = di.range_id
		WHERE di.server = ? AND di.item_id IS NOT NULL
		GROUP BY di.stage_id, di.item_id, tr.start_time
		HAVING count(DISTINCT di.range_id) > 1`, server).Scan(context.Background(), &rows)
	if err != nil {
		t.Fatal(err)
	}
	prefixes := make(map[string]bool, len(rows))
	for _, row := range rows {
		prefixes[fmt.Sprintf("%d|%d|", row.StageID, row.ItemID)] = true
	}
	return prefixes
}

func withoutStageItems(lines []string, prefixes map[string]bool) []string {
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "|", 3)
		if !prefixes[parts[0]+"|"+parts[1]+"|"] {
			kept = append(kept, line)
		}
	}
	return kept
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

// personalDropMatrixElementLines are the lines of drop matrix elements of time ranges: those of a customized time
// range (range id 0) carry it.
func personalDropMatrixElementLines(elements []*model.DropMatrixElement) []string {
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
		timeRange := ""
		if el.TimeRange != nil {
			timeRange = timeRangeLine(el.TimeRange)
		}
		lines = append(lines, fmt.Sprintf("%s|%s|%d|%d|%d|%d|%d|%s|%s", el.Server, el.SourceCategory, el.StageID, el.ItemID,
			el.RangeID, el.Quantity, el.Times, timeRange, b.String()))
	}
	sort.Strings(lines)
	return lines
}

func TestCalcDropMatrixForTimeRangesMatchesLegacy(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	sourceCategories := []string{constant.SourceCategoryAll, constant.SourceCategoryManual, constant.SourceCategoryAutomated}
	compared := 0
	for seed := int64(1); seed <= 3; seed++ {
		mustExec(t, db, "TRUNCATE time_ranges, drop_infos, drop_reports, drop_pattern_elements")
		r := rand.New(rand.NewSource(seed))
		seedMatrixData(t, r, db)
		s := newMatrixServices(db)
		for _, server := range testServers {
			// a personal matrix: every max accumulable time range, as getDropMatrixElements collects them
			maxAccumulableTimeRanges, err := s.timeRange.GetMaxAccumulableTimeRangesByServer(ctx, server)
			if err != nil {
				t.Fatal(err)
			}
			timeRangesMap := make(map[int]*model.TimeRange)
			for _, byItem := range maxAccumulableTimeRanges {
				for _, timeRanges := range byItem {
					for _, timeRange := range timeRanges {
						timeRangesMap[timeRange.RangeID] = timeRange
					}
				}
			}
			personal := make([]*model.TimeRange, 0, len(timeRangesMap))
			for _, timeRange := range timeRangesMap {
				personal = append(personal, timeRange)
			}
			// customized time ranges (advanced queries) at millisecond precision, one of them inverted
			customized := make([][]*model.TimeRange, 0)
			for i := 0; i < 3; i++ {
				start := testBaseTime.Add(time.Duration(r.Int63n(int64(4*24*time.Hour))) / time.Millisecond * time.Millisecond)
				end := start.Add(time.Duration(1+r.Intn(48)) * time.Hour).Add(time.Duration(r.Intn(1000)) * time.Millisecond)
				if i == 2 {
					start, end = end, start
				}
				customized = append(customized, []*model.TimeRange{{StartTime: &start, EndTime: &end}})
			}

			for _, accountId := range []null.Int{{}, null.IntFrom(1), null.IntFrom(2), null.IntFrom(3)} {
				for _, sourceCategory := range sourceCategories {
					what := fmt.Sprintf("seed %d %s account %v %s", seed, server, accountId.Int64, sourceCategory)
					// the legacy loop runs two queries per time range: compare the personal matrices on one seed and
					// two accounts only
					if seed == 1 && accountId.Int64 <= 1 {
						want, err := legacyCalcDropMatrixForTimeRanges(ctx, s.dropMatrix, server, personal, nil, nil, accountId, sourceCategory)
						if err != nil {
							t.Fatal(err)
						}
						got, err := s.dropMatrix.calcDropMatrixForTimeRanges(ctx, server, personal, nil, nil, accountId, sourceCategory)
						if err != nil {
							t.Fatal(err)
						}
						compared += assertSameLines(t, what+" personal", personalDropMatrixElementLines(want), personalDropMatrixElementLines(got))
					}

					for i, timeRanges := range customized {
						for _, itemIdFilter := range [][]int{nil, {10, 12, 14}} {
							stageIdFilter := []int{1 + r.Intn(6)}
							what := fmt.Sprintf("%s customized %d stages %v items %v", what, i, stageIdFilter, itemIdFilter)
							want, err := legacyCalcDropMatrixForTimeRanges(ctx, s.dropMatrix, server, timeRanges, stageIdFilter, itemIdFilter, accountId, sourceCategory)
							if err != nil {
								t.Fatal(err)
							}
							got, err := s.dropMatrix.calcDropMatrixForTimeRanges(ctx, server, timeRanges, stageIdFilter, itemIdFilter, accountId, sourceCategory)
							if err != nil {
								t.Fatal(err)
							}
							compared += assertSameLines(t, what, personalDropMatrixElementLines(want), personalDropMatrixElementLines(got))
						}
					}
				}
			}
		}
	}
	requireCompared(t, compared, 500)
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
