package repo

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
)

var allSourceCategories = []string{constant.SourceCategoryAll, constant.SourceCategoryManual, constant.SourceCategoryAutomated}

// dropReportFixture describes the reports seeded by seedDropReports.
type dropReportFixture struct {
	base       time.Time
	boundaries []time.Time
	accountIds []int64
	stageIds   []int
}

// seedDropReports inserts random drop patterns and reports of several servers, accounts, sources and reliabilities.
// Many reports are created on, just before or just after the fixture's boundaries (with sub-second offsets), where
// interval and segment bounds are easiest to get wrong.
func seedDropReports(t *testing.T, r *rand.Rand, db *bun.DB) dropReportFixture {
	t.Helper()
	return seedDropReportsFrom(t, r, db, time.Date(2026, 10, 19, 4, 0, 0, 0, time.UTC))
}

// seedDropReportsFrom is seedDropReports with boundaries every 3 hours for 24 hours from base.
func seedDropReportsFrom(t *testing.T, r *rand.Rand, db *bun.DB, base time.Time) dropReportFixture {
	t.Helper()
	f := dropReportFixture{
		base:       base,
		accountIds: []int64{1, 2, 3},
		stageIds:   []int{1, 2, 3, 4, 5},
	}
	for i := 0; i <= 8; i++ {
		f.boundaries = append(f.boundaries, base.Add(time.Duration(i)*3*time.Hour))
	}

	// pattern 1 is the empty pattern: it has no elements
	for patternId := 2; patternId <= 20; patternId++ {
		for itemId := 1; itemId <= 1+r.Intn(4); itemId++ {
			if r.Intn(3) != 0 {
				mustExec(t, db, "INSERT INTO drop_pattern_elements (drop_pattern_id, item_id, quantity) VALUES (?, ?, ?)",
					patternId, 10+itemId, 1+r.Intn(3))
			}
		}
	}

	sources := []interface{}{nil, constant.ManualSources[0], constant.ManualSources[len(constant.ManualSources)-1], "MeoAssistant", "arknights-auto"}
	servers := []string{"CN", "CN", "CN", "US"}
	span := f.boundaries[len(f.boundaries)-1].Sub(base)
	for i := 0; i < 3000; i++ {
		var createdAt time.Time
		switch r.Intn(3) {
		case 0: // anywhere, including before the first and after the last boundary
			createdAt = base.Add(-time.Hour + time.Duration(r.Int63n(int64(span+2*time.Hour))))
		default: // around a boundary
			boundary := f.boundaries[r.Intn(len(f.boundaries))]
			createdAt = boundary.Add(time.Duration(r.Intn(5)-2) * time.Duration(r.Intn(2)+1) * 500 * time.Millisecond)
		}
		reliability := 0
		if r.Intn(6) == 0 {
			reliability = []int{-1, 1, 2}[r.Intn(3)]
		}
		times := 1
		if r.Intn(5) == 0 {
			times = 2 + r.Intn(5)
		}
		mustExec(t, db, `INSERT INTO drop_reports (stage_id, pattern_id, times, created_at, reliability, server, account_id, source_name)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			f.stageIds[r.Intn(len(f.stageIds))], 1+r.Intn(20), times, createdAt, reliability,
			servers[r.Intn(len(servers))], f.accountIds[r.Intn(len(f.accountIds))], sources[r.Intn(len(sources))])
	}
	return f
}

// randomIntervals picks intervals between fixture boundaries: overlapping, nested, adjacent and duplicated ones, with
// random stage filters (sometimes none).
func randomIntervals(r *rand.Rand, f dropReportFixture, server string, accountId null.Int, excludeNonOneTimes bool) []*model.DropReportQueryContext {
	queryCtxs := make([]*model.DropReportQueryContext, 0)
	for i := 0; i < 2+r.Intn(8); i++ {
		from := r.Intn(len(f.boundaries) - 1)
		to := from + 1 + r.Intn(len(f.boundaries)-1-from)
		start, end := f.boundaries[from], f.boundaries[to]
		queryCtx := &model.DropReportQueryContext{
			Server:             server,
			StartTime:          &start,
			EndTime:            &end,
			AccountID:          accountId,
			ExcludeNonOneTimes: excludeNonOneTimes,
		}
		if r.Intn(4) != 0 {
			filter := make(map[int][]int)
			for _, stageId := range f.stageIds {
				if r.Intn(2) == 0 {
					filter[stageId] = []int{}
				}
			}
			if len(filter) == 0 {
				filter[f.stageIds[0]] = []int{}
			}
			queryCtx.StageItemFilter = &filter
		}
		queryCtxs = append(queryCtxs, queryCtx)
	}
	return queryCtxs
}

func TestCalcDropMatrixForIntervalsBySourceCategoriesMatchesPerIntervalQueries(t *testing.T) {
	db := newTestDB(t)
	repo := NewDropReport(db)
	ctx := context.Background()
	r := rand.New(rand.NewSource(1))
	f := seedDropReports(t, r, db)

	for round := 0; round < 24; round++ {
		accountId := null.Int{}
		if round%4 == 3 {
			accountId = null.IntFrom(f.accountIds[r.Intn(len(f.accountIds))])
		}
		server := []string{"CN", "US"}[round%2]
		queryCtxs := randomIntervals(r, f, server, accountId, round%3 == 0)

		timesResults, quantityResults, err := repo.CalcDropMatrixForIntervalsBySourceCategories(ctx, queryCtxs, allSourceCategories)
		if err != nil {
			t.Fatal(err)
		}
		if len(timesResults) != len(queryCtxs) || len(quantityResults) != len(queryCtxs) {
			t.Fatalf("round %d: got %d/%d results for %d intervals", round, len(timesResults), len(quantityResults), len(queryCtxs))
		}
		for i, queryCtx := range queryCtxs {
			for _, sourceCategory := range allSourceCategories {
				single := *queryCtx
				single.SourceCategory = sourceCategory

				wantTimes, err := repo.CalcTotalTimes(ctx, &single)
				if err != nil {
					t.Fatal(err)
				}
				assertSameCounts(t, "times", round, i, sourceCategory, totalTimesByStage(wantTimes), totalTimesByStage(timesResults[i][sourceCategory]))

				wantQuantities, err := repo.CalcQuantityUniqCount(ctx, &single)
				if err != nil {
					t.Fatal(err)
				}
				assertSameCounts(t, "quantity uniq counts", round, i, sourceCategory,
					quantityUniqCounts(wantQuantities), quantityUniqCounts(quantityResults[i][sourceCategory]))
			}
		}
	}
}

func TestCalcDropMatrixForIntervalsBySourceCategoriesWithoutIntervals(t *testing.T) {
	db := newTestDB(t)
	timesResults, quantityResults, err := NewDropReport(db).CalcDropMatrixForIntervalsBySourceCategories(context.Background(), nil, allSourceCategories)
	if err != nil || timesResults != nil || quantityResults != nil {
		t.Fatalf("got %v, %v, %v", timesResults, quantityResults, err)
	}
}

func TestCalcTotalQuantityForPatternMatrixBySourceCategoriesMatchesPerCategoryQueries(t *testing.T) {
	db := newTestDB(t)
	repo := NewDropReport(db)
	ctx := context.Background()
	r := rand.New(rand.NewSource(2))
	f := seedDropReports(t, r, db)

	for round := 0; round < 16; round++ {
		accountId := null.Int{}
		if round%4 == 3 {
			accountId = null.IntFrom(f.accountIds[r.Intn(len(f.accountIds))])
		}
		for _, queryCtx := range randomIntervals(r, f, []string{"CN", "US"}[round%2], accountId, false) {
			got, err := repo.CalcTotalQuantityForPatternMatrixBySourceCategories(ctx, queryCtx, allSourceCategories)
			if err != nil {
				t.Fatal(err)
			}
			for _, sourceCategory := range allSourceCategories {
				single := *queryCtx
				single.SourceCategory = sourceCategory
				want, err := repo.CalcTotalQuantityForPatternMatrix(ctx, &single)
				if err != nil {
					t.Fatal(err)
				}
				assertSameCounts(t, "pattern quantities", round, 0, sourceCategory, patternQuantities(want), patternQuantities(got[sourceCategory]))
			}
		}
	}
}

func assertSameCounts[K comparable](t *testing.T, what string, round, interval int, sourceCategory string, want, got map[K]int) {
	t.Helper()
	if len(want) == 0 {
		t.Logf("round %d interval %d %s: no %s", round, interval, sourceCategory, what)
	}
	if len(got) != len(want) {
		t.Fatalf("round %d interval %d %s: %d %s, want %d\ngot  %v\nwant %v", round, interval, sourceCategory, len(got), what, len(want), got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("round %d interval %d %s: %s of %v = %d, want %d", round, interval, sourceCategory, what, k, got[k], v)
		}
	}
}

// the result helpers fail on duplicate keys by keeping the count of an extra row negative
func totalTimesByStage(results []*model.TotalTimesResult) map[int]int {
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

func quantityUniqCounts(results []*model.QuantityUniqCountResultForDropMatrix) map[[3]int]int {
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

func patternQuantities(results []*model.TotalQuantityResultForPatternMatrix) map[[2]int]int {
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
