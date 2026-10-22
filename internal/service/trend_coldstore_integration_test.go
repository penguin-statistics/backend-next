package service

// Integration test of the customized trend split at the hot boundary of the cold storage (#752), on the data of the
// drop matrix's test (drop_report_coldstore_integration_test.go). Skipped unless PENGUIN_TEST_POSTGRES_DSN and
// PENGUIN_TEST_DUCKDB_EXTENSION_DIR are set.

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo"
)

// coldTestTrend is a trend query: intervalNum+1 buckets of intervalLength from the game day of start.
type coldTestTrend struct {
	name           string
	start          time.Time
	intervalLength time.Duration
	intervalNum    int
}

// coldTestTrends returns trends fully before and fully after the hot boundary, across it with buckets that start at
// it and with buckets that straddle it, and from before any report.
func coldTestTrends() []coldTestTrend {
	return []coldTestTrend{
		{"cold days", time.Date(2019, 5, 1, 0, 0, 0, 0, time.UTC), 24 * time.Hour, 20},
		{"cold hours", time.Date(2019, 6, 10, 0, 0, 0, 0, time.UTC), time.Hour, 30},
		{"cold up to the boundary", time.Date(2019, 6, 20, 12, 0, 0, 0, time.UTC), 24 * time.Hour, 9},
		// CN and US game days start at 20:00 and 11:00 UTC: buckets of 5 hours straddle the boundary
		{"across, straddling buckets of 5 hours", time.Date(2019, 6, 28, 12, 0, 0, 0, time.UTC), 5 * time.Hour, 30},
		{"across, straddling buckets of 7 hours", time.Date(2019, 6, 29, 12, 0, 0, 0, time.UTC), 7 * time.Hour, 12},
		{"across, hourly buckets", time.Date(2019, 6, 30, 6, 0, 0, 0, time.UTC), time.Hour, 48},
		{"across, weekly buckets", time.Date(2019, 4, 15, 0, 0, 0, 0, time.UTC), 7 * 24 * time.Hour, 14},
		{"across, one bucket", time.Date(2019, 6, 30, 12, 0, 0, 0, time.UTC), 48 * time.Hour, 0},
		{"from before any report", time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC), 30 * 24 * time.Hour, 8},
		{"hot", time.Date(2019, 7, 2, 0, 0, 0, 0, time.UTC), 24 * time.Hour, 10},
	}
}

func TestTrendSplitAtTheHotBoundaryMatchesPostgres(t *testing.T) {
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

	// the test covers buckets that straddle the boundary, for both servers
	for _, server := range []string{"CN", "US"} {
		trend := coldTestTrends()[3]
		start, _ := repo.TrendTimeRange(server, trend.start, trend.intervalLength, trend.intervalNum)
		if coldTestBoundary.Sub(start)%trend.intervalLength == 0 {
			t.Fatalf("%s: %s: the boundary is a bucket boundary", server, trend.name)
		}
	}

	type mode struct {
		name      string
		server    string
		accountId null.Int
	}
	modes := []mode{
		{"CN", "CN", null.Int{}},
		{"US", "US", null.Int{}},
		{"CN account 1", "CN", null.IntFrom(1)},
		{"US account 2", "US", null.IntFrom(2)},
	}
	// stages with items, as GetStageIdItemIdMapFromDropInfos returns them, the recruit stage among them
	filters := []map[int][]int{
		{1: {11, 12}, 2: {11, 12, 13, 14}},
		{coldTestRecruitStageID: {11, 12, 13, 14}},
		{3: {13}, 4: {11, 14}, coldTestRecruitStageID: {12}},
		{1: {11, 12, 13, 14}, 2: {11, 12, 13, 14}, 3: {11, 12, 13, 14}, 4: {11, 12, 13, 14}, 5: {11, 12, 13, 14}, coldTestRecruitStageID: {11, 12, 13, 14}},
	}
	sourceCategories := []string{constant.SourceCategoryAll, constant.SourceCategoryManual, constant.SourceCategoryAutomated}

	type query struct {
		name           string
		mode           mode
		trend          coldTestTrend
		stageIdItemIds map[int][]int
		stageIds       []int
		sourceCategory string
		want           []string
	}
	lines := func(dropReport *DropReport, q query) ([]string, error) {
		quantities, err := dropReport.CalcTotalQuantityForTrend(ctx, q.mode.server, &q.trend.start, q.trend.intervalLength, q.trend.intervalNum, q.stageIdItemIds, q.mode.accountId, q.sourceCategory)
		if err != nil {
			return nil, err
		}
		times, err := dropReport.CalcTotalTimesForTrend(ctx, q.mode.server, &q.trend.start, q.trend.intervalLength, q.trend.intervalNum, q.stageIds, q.mode.accountId, q.sourceCategory)
		if err != nil {
			return nil, err
		}
		return coldTestTrendLines(quantities, times), nil
	}
	var queries []query
	for _, m := range modes {
		for fi, filter := range filters {
			stageIds := make([]int, 0, len(filter))
			for stageId := range filter {
				stageIds = append(stageIds, stageId)
			}
			for _, trend := range coldTestTrends() {
				for _, sourceCategory := range sourceCategories {
					q := query{
						name: fmt.Sprintf("%s, filter %d, %s, %s", m.name, fi, trend.name, sourceCategory),
						mode: m, trend: trend, stageIdItemIds: filter, stageIds: stageIds, sourceCategory: sourceCategory,
					}
					if q.want, err = lines(pgOnly, q); err != nil {
						t.Fatal(err)
					}
					queries = append(queries, q)
				}
			}
		}
	}

	// the trends of a stage through to the combined results, with the drop infos of a time range of the whole data. A
	// trend of a stage, as /result/advanced queries it: with several stages, combineQuantityAndTimesResults gives the
	// zero quantities of a bucket without drops to the first stage it sees only, which depends on the order of the
	// results, in PostgreSQL alone too.
	newTrend := func(dropReport *DropReport) *Trend {
		dropInfoRepo := repo.NewDropInfo(db)
		return &Trend{DropReportService: dropReport, DropInfoService: NewDropInfo(dropInfoRepo, NewTimeRange(repo.NewTimeRange(db), dropInfoRepo))}
	}
	pgOnlyTrends, splitTrends := newTrend(pgOnly), newTrend(split)
	for _, server := range []string{"CN", "US"} {
		var rangeId int
		if err := db.NewRaw("INSERT INTO time_ranges (name, start_time, end_time, server) VALUES (?, ?, ?, ?) RETURNING range_id",
			server, time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), server).Scan(ctx, &rangeId); err != nil {
			t.Fatal(err)
		}
		for _, stageId := range []int{1, 2, 3, coldTestRecruitStageID} {
			for _, itemId := range []int{11, 12, 13} {
				mustExec(t, db, "INSERT INTO drop_infos (server, stage_id, item_id, drop_type, range_id, accumulable) VALUES (?, ?, ?, 'REGULAR', ?, true)",
					server, stageId, itemId, rangeId)
			}
		}
	}
	type trendQuery struct {
		name      string
		mode      mode
		trend     coldTestTrend
		stageIds  []int
		itemIds   []int
		category  string
		wantLines []string
	}
	var trendQueries []trendQuery
	for _, m := range modes {
		for _, trend := range coldTestTrends() {
			for _, stageIds := range [][]int{{1}, {3}, {coldTestRecruitStageID}} {
				for _, category := range []string{constant.SourceCategoryAll, constant.SourceCategoryAutomated} {
					q := trendQuery{name: fmt.Sprintf("trend of %s, %s, stages %v, %s", m.name, trend.name, stageIds, category),
						mode: m, trend: trend, stageIds: stageIds, itemIds: []int{11, 13}, category: category}
					result, err := pgOnlyTrends.queryTrend(ctx, m.server, &trend.start, trend.intervalLength, trend.intervalNum, stageIds, q.itemIds, m.accountId, category)
					if err != nil {
						t.Fatal(err)
					}
					q.wantLines = coldTestTrendResultLines(result)
					trendQueries = append(trendQueries, q)
				}
			}
		}
	}

	compare := func(stage string) {
		t.Helper()
		compared := 0
		for _, q := range queries {
			got, err := lines(split, q)
			if err != nil {
				t.Fatalf("%s: %s: %v", stage, q.name, err)
			}
			compared += assertSameLines(t, stage+": "+q.name, q.want, got)
		}
		requireCompared(t, compared, 10000)
		compared = 0
		for _, q := range trendQueries {
			result, err := splitTrends.queryTrend(ctx, q.mode.server, &q.trend.start, q.trend.intervalLength, q.trend.intervalNum, q.stageIds, q.itemIds, q.mode.accountId, q.category)
			if err != nil {
				t.Fatalf("%s: %s: %v", stage, q.name, err)
			}
			compared += assertSameLines(t, stage+": "+q.name, q.wantLines, coldTestTrendResultLines(result))
		}
		requireCompared(t, compared, 500)
	}
	// shadow mode: PostgreSQL still holds every report, and only the part from the boundary on is read from it
	compare("all in PostgreSQL")
	// after the migration: the archived reports are only in the cold storage
	mustExec(t, db, "DELETE FROM drop_reports WHERE created_at < ?", coldTestBoundary)
	compare("archived reports deleted from PostgreSQL")

	// the cold storage alone refuses a part past its hot boundary
	trend := coldTestTrends()[3]
	start, end := repo.TrendTimeRange("CN", trend.start, trend.intervalLength, trend.intervalNum)
	if _, err := coldRepo.CalcTotalTimesForTrend(ctx, coldRepo.View(), start, end, "CN", &trend.start, trend.intervalLength, trend.intervalNum,
		[]int{1}, null.Int{}, constant.SourceCategoryAll); err == nil {
		t.Error("a trend past the hot boundary was read from the cold storage")
	}
}

// coldTestTrendLines returns canonical lines of the quantity and times results of a trend, keeping duplicates.
func coldTestTrendLines(quantities []*model.TotalQuantityResultForTrend, times []*model.TotalTimesResultForTrend) []string {
	lines := make([]string, 0, len(quantities)+len(times))
	for _, q := range quantities {
		lines = append(lines, fmt.Sprintf("quantity|%d|%d|%d|%d|%d|%d", q.GroupID, q.IntervalStart.Unix(), q.IntervalEnd.Unix(), q.StageID, q.ItemID, q.TotalQuantity))
	}
	for _, t := range times {
		lines = append(lines, fmt.Sprintf("times|%d|%d|%d|%d|%d", t.GroupID, t.IntervalStart.Unix(), t.IntervalEnd.Unix(), t.StageID, t.TotalTimes))
	}
	sort.Strings(lines)
	return lines
}

// coldTestTrendResultLines returns canonical lines of the trends of a query, one per stage, item and bucket.
func coldTestTrendResultLines(result *model.TrendQueryResult) []string {
	var lines []string
	for _, stageTrend := range result.Trends {
		for _, itemTrend := range stageTrend.Results {
			for groupId := range itemTrend.Times {
				lines = append(lines, fmt.Sprintf("%d|%d|%d|%d-%d|%d|%d|%d", stageTrend.StageID, itemTrend.ItemID, itemTrend.StartTime.Unix(),
					itemTrend.MinGroupID, itemTrend.MaxGroupID, groupId, itemTrend.Times[groupId], itemTrend.Quantity[groupId]))
			}
		}
	}
	sort.Strings(lines)
	return lines
}
