package service

// Integration test of the pattern matrix split at the hot boundary of the cold storage (#752), on the data of the
// drop matrix's test (drop_report_coldstore_integration_test.go). Skipped unless PENGUIN_TEST_POSTGRES_DSN and
// PENGUIN_TEST_DUCKDB_EXTENSION_DIR are set.

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo"
)

func TestPatternMatrixSplitAtTheHotBoundaryMatchesPostgres(t *testing.T) {
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

	// every interval of the drop matrix's queries, one at a time as the pattern matrix queries them; their times
	// filter does not matter, as the pattern matrix counts only reports with times = 1 either way
	var queryCtxs []*model.DropReportQueryContext
	var names []string
	for _, q := range coldTestQueries(r) {
		for j, queryCtx := range q.queryCtxs {
			queryCtxs = append(queryCtxs, queryCtx)
			names = append(names, fmt.Sprintf("%s, interval %d", q.name, j))
		}
	}
	want := make([]map[string][]*model.TotalQuantityResultForPatternMatrix, len(queryCtxs))
	nonEmpty := 0
	for i, queryCtx := range queryCtxs {
		if want[i], err = pgOnly.CalcTotalQuantityForPatternMatrixBySourceCategories(ctx, queryCtx, sourceCategories); err != nil {
			t.Fatal(err)
		}
		if len(want[i][constant.SourceCategoryAll]) > 0 {
			nonEmpty++
		}
	}
	if nonEmpty < len(queryCtxs)*3/4 {
		t.Fatalf("only %d of %d intervals count reports", nonEmpty, len(queryCtxs))
	}

	// the elements of the pattern matrix, from the drop infos of the stages through to the times of each stage
	patternMatrix := func(dropReport *DropReport) *PatternMatrix {
		dropInfoRepo := repo.NewDropInfo(db)
		return &PatternMatrix{DropReportService: dropReport, DropInfoService: NewDropInfo(dropInfoRepo, NewTimeRange(repo.NewTimeRange(db), dropInfoRepo))}
	}
	pgOnlyMatrix, splitMatrix := patternMatrix(pgOnly), patternMatrix(split)
	var timeRanges []*model.TimeRange
	for _, server := range []string{"CN", "US"} {
		for i, tr := range [][2]time.Time{
			{coldTestTimes[0], coldTestTimes[14]}, // across the boundary
			{coldTestTimes[4], coldTestTimes[10]}, // cold
		} {
			var rangeId int
			if err := db.NewRaw("INSERT INTO time_ranges (name, start_time, end_time, server) VALUES (?, ?, ?, ?) RETURNING range_id",
				fmt.Sprintf("%s-%d", server, i), tr[0], tr[1], server).Scan(ctx, &rangeId); err != nil {
				t.Fatal(err)
			}
			for _, stageId := range []int{1, 2, 3} {
				mustExec(t, db, "INSERT INTO drop_infos (server, stage_id, item_id, drop_type, range_id, accumulable) VALUES (?, ?, 11, 'REGULAR', ?, true)",
					server, stageId, rangeId)
			}
			start, end := tr[0], tr[1]
			timeRanges = append(timeRanges, &model.TimeRange{RangeID: rangeId, Server: server, StartTime: &start, EndTime: &end})
		}
	}

	type elementsQuery struct {
		timeRange *model.TimeRange
		accountId null.Int
		want      []string
	}
	var elementsQueries []elementsQuery
	for _, tr := range timeRanges {
		for _, accountId := range []null.Int{{}, null.IntFrom(1)} {
			elements, err := pgOnlyMatrix.calcPatternMatrixForTimeRanges(ctx, tr.Server, []*model.TimeRange{tr}, []int{1, 2, 3}, accountId, sourceCategories)
			if err != nil {
				t.Fatal(err)
			}
			elementsQueries = append(elementsQueries, elementsQuery{tr, accountId, patternMatrixElementLines(elements)})
		}
	}

	compare := func(stage string) {
		t.Helper()
		for i, queryCtx := range queryCtxs {
			got, err := split.CalcTotalQuantityForPatternMatrixBySourceCategories(ctx, queryCtx, sourceCategories)
			if err != nil {
				t.Fatalf("%s: %s: %v", stage, names[i], err)
			}
			for _, sourceCategory := range sourceCategories {
				assertSameColdTestCounts(t, fmt.Sprintf("%s: %s, %s", stage, names[i], sourceCategory),
					coldTestPatternQuantities(want[i][sourceCategory]), coldTestPatternQuantities(got[sourceCategory]))
			}
		}
		compared := 0
		for _, q := range elementsQueries {
			what := fmt.Sprintf("%s: pattern matrix of %s %s, account %v", stage, q.timeRange.Server, q.timeRange.String(), q.accountId.Ptr())
			elements, err := splitMatrix.calcPatternMatrixForTimeRanges(ctx, q.timeRange.Server, []*model.TimeRange{q.timeRange}, []int{1, 2, 3}, q.accountId, sourceCategories)
			if err != nil {
				t.Fatalf("%s: %v", what, err)
			}
			compared += assertSameLines(t, what, q.want, patternMatrixElementLines(elements))
		}
		requireCompared(t, compared, 100)
	}
	// shadow mode: PostgreSQL still holds every report, and only the part from the boundary on is read from it
	compare("all in PostgreSQL")
	// after the migration: the archived reports are only in the cold storage
	mustExec(t, db, "DELETE FROM drop_reports WHERE created_at < ?", coldTestBoundary)
	compare("archived reports deleted from PostgreSQL")

	// the cold storage alone refuses an interval past its hot boundary
	start, end := coldTestTimes[10], coldTestTimes[13]
	if _, err := coldRepo.CalcTotalQuantityForPatternMatrixBySourceCategories(ctx, coldRepo.View(),
		&model.DropReportQueryContext{Server: "CN", StartTime: &start, EndTime: &end}, sourceCategories); err == nil {
		t.Error("an interval past the hot boundary was read from the cold storage")
	}
}

// coldTestPatternQuantities fails on duplicate keys as the drop matrix's result helpers do.
func coldTestPatternQuantities(results []*model.TotalQuantityResultForPatternMatrix) map[[2]int]int {
	m := make(map[[2]int]int)
	for _, result := range results {
		k := [2]int{result.StageID, result.PatternID}
		if _, ok := m[k]; ok {
			m[k] = -1
			continue
		}
		m[k] = result.TotalQuantity
	}
	return m
}
