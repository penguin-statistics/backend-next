package service

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/util"
)

// flushCachesKeepingBases empties every cache but the bases of the incremental global drop matrices, as a refresh
// finds them after the caches of the time ranges and drop infos expired.
func flushCachesKeepingBases(t *testing.T) {
	t.Helper()
	cache.Initialize()
	for name, flush := range cache.SetMap {
		if name == "globalDropMatrixBase#server|sourceCategory" {
			continue
		}
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

// incrementalDropMatrix returns a copy of off with its own configuration, calculating the global drop matrices
// incrementally.
func incrementalDropMatrix(off *DropMatrix, mode appconfig.GlobalMatrixIncrementalMode) *DropMatrix {
	conf := *off.Config
	conf.GlobalMatrixIncremental = mode
	conf.GlobalMatrixBaseMaxAge = 1000 * time.Hour
	conf.GlobalMatrixMaxChangedStages = 2
	return &DropMatrix{
		Config: &conf, TimeRangeService: off.TimeRangeService, DropReportService: off.DropReportService, DropInfoService: off.DropInfoService,
		DropMatrixElementService: off.DropMatrixElementService, StageService: off.StageService, ItemService: off.ItemService,
		SiteStatsService: off.SiteStatsService,
	}
}

// insertReports inserts n random reports of server created in [from, to).
func insertReports(t *testing.T, r *rand.Rand, db *bun.DB, server string, from, to time.Time, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		times := 1 + r.Intn(3)
		source := []interface{}{nil, constant.ManualSources[0], "MeoAssistant"}[r.Intn(3)]
		mustExec(t, db, `INSERT INTO drop_reports (stage_id, pattern_id, times, created_at, reliability, server, account_id, source_name)
			VALUES (?, ?, ?, ?, 0, ?, ?, ?)`,
			1+r.Intn(6), 1+r.Intn(25), times, from.Add(time.Duration(r.Int63n(int64(to.Sub(from))))), server, 1+r.Intn(3), source)
	}
}

// refreshedDropMatrices refreshes the global drop matrices of every server with s, and returns them as cached: raw
// and shim, with and without closed zones, of every source category it refreshes.
func refreshedDropMatrices(t *testing.T, s *DropMatrix) map[string]string {
	t.Helper()
	ctx := context.Background()
	results := make(map[string]string)
	for _, server := range testServers {
		if err := s.RefreshGlobalDropMatrixCaches(ctx, server); err != nil {
			t.Fatal(err)
		}
		for _, sourceCategory := range s.Config.MatrixWorkerSourceCategories {
			var raw model.DropMatrixQueryResult
			if err := cache.GlobalDropMatrix.Get(server+constant.CacheSep+sourceCategory, &raw); err != nil {
				t.Fatal(err)
			}
			results[server+" "+sourceCategory+" raw"] = fmt.Sprint(globalDropMatrixLines(&raw))
			for _, showClosedZones := range []bool{true, false} {
				var shim modelv2.DropMatrixQueryResult
				key := server + constant.CacheSep + strconv.FormatBool(showClosedZones) + constant.CacheSep + sourceCategory
				if err := cache.ShimGlobalDropMatrix.Get(key, &shim); err != nil {
					t.Fatal(err)
				}
				results[server+" "+sourceCategory+" shim "+strconv.FormatBool(showClosedZones)] = canonicalJSON(t, shim.Matrix)
			}
		}
	}
	return results
}

func globalMatrixPathCounts() map[string]float64 {
	counts := make(map[string]float64)
	for _, server := range testServers {
		for _, sourceCategory := range allSourceCategories {
			for _, pr := range [][2]string{
				{globalMatrixPathIncremental, globalMatrixReasonNone},
				{globalMatrixPathRebuild, globalMatrixReasonNoBase}, {globalMatrixPathRebuild, globalMatrixReasonFingerprint},
				{globalMatrixPathRebuild, globalMatrixReasonDayBack}, {globalMatrixPathRebuild, globalMatrixReasonMaxAge},
				{globalMatrixPathRebuild, globalMatrixReasonChangedStages}, {globalMatrixPathRebuild, globalMatrixReasonTriggersMissing},
				{globalMatrixPathFull, globalMatrixReasonTriggersMissing},
			} {
				counts[pr[0]+" "+pr[1]] += testutil.ToFloat64(observability.GlobalMatrixRefreshPaths.WithLabelValues(globalMatrixKindDrop, server, sourceCategory, pr[0], pr[1]))
			}
		}
	}
	return counts
}

func globalMatrixVerifyMismatchCount() float64 {
	var n float64
	for _, server := range testServers {
		for _, sourceCategory := range allSourceCategories {
			n += testutil.ToFloat64(observability.GlobalMatrixVerifyMismatches.WithLabelValues(globalMatrixKindDrop, server, sourceCategory))
		}
	}
	return n
}

// TestGlobalDropMatrixIncrementalMatchesOff runs random sequences of everything that changes the global drop
// matrices: the worker's batches as the clock goes on, admin recalculations of old days, today and yesterday, SQL
// repairs of old elements, a TRUNCATE and reload, writes while a trigger is disabled, edits of the time ranges and
// drop infos (an end, an accumulable flag, a time range or a target added, a target removed), changes of the source
// categories of the worker and an expired base. After each, the incremental global drop matrices, raw and shim, are
// those calculated without bases.
func TestGlobalDropMatrixIncrementalMatchesOff(t *testing.T) {
	db, off, _ := newGlobalMatrixTestData(t)
	seedOpeningStages(t, db)
	ctx := context.Background()
	r := rand.New(rand.NewSource(860))
	off.SiteStatsService = &SiteStats{baseGenerations: &memorySiteStatsBaseGenerations{}}
	on := incrementalDropMatrix(off, appconfig.GlobalMatrixIncrementalOn)
	flushCaches(t)
	pathsBefore := globalMatrixPathCounts()
	mismatchesBefore := globalMatrixVerifyMismatchCount()

	compared := 0
	compare := func(step string) {
		t.Helper()
		flushCachesKeepingBases(t)
		want := refreshedDropMatrices(t, off)
		got := refreshedDropMatrices(t, on)
		for key, w := range want {
			if got[key] != w {
				t.Fatalf("%s: %s differs from the one calculated without bases\ngot  %.600s\nwant %.600s", step, key, got[key], w)
			}
			if w != "[]" {
				compared++
			}
		}
		// a request of a single source category, maybe one the worker does not refresh
		server, sourceCategory := testServers[r.Intn(len(testServers))], allSourceCategories[r.Intn(len(allSourceCategories))]
		w, err := off.calcGlobalDropMatrixUncached(ctx, server, sourceCategory)
		if err != nil {
			t.Fatal(err)
		}
		g, err := on.calcGlobalDropMatrixUncached(ctx, server, sourceCategory)
		if err != nil {
			t.Fatal(err)
		}
		assertSameLines(t, step+" request "+server+" "+sourceCategory, globalDropMatrixLines(w), globalDropMatrixLines(g))
	}
	setNow := func(now time.Time) { util.Now = func() time.Time { return now } }
	randomElement := func(server string, beforeDayNum int) int64 {
		var ids []int64
		if err := db.NewRaw("SELECT element_id FROM drop_matrix_elements WHERE server = ? AND day_num < ? AND quantity > 0 ORDER BY element_id",
			server, beforeDayNum).Scan(ctx, &ids); err != nil {
			t.Fatal(err)
		}
		if len(ids) == 0 {
			t.Fatalf("no element of %s before day %d", server, beforeDayNum)
		}
		return ids[r.Intn(len(ids))]
	}
	randomId := func(query string, args ...interface{}) int64 {
		var ids []int64
		if err := db.NewRaw(query, args...).Scan(ctx, &ids); err != nil {
			t.Fatal(err)
		}
		if len(ids) == 0 {
			t.Fatalf("no row for %s", query)
		}
		return ids[r.Intn(len(ids))]
	}

	compare("start")
	ops := map[string]func(step string){
		"batch": func(step string) {
			now := util.Now()
			for _, server := range testServers {
				insertReports(t, r, db, server, time.UnixMilli(util.GetDayStartTime(&now, server)), now, 20)
				if err := off.RunCalcDropMatrixJob(ctx, server); err != nil {
					t.Fatal(err)
				}
			}
			compare(step)
		},
		"next day": func(step string) {
			setNow(util.Now().Add(time.Duration(6+r.Intn(30)) * time.Hour))
			now := util.Now()
			for _, server := range testServers {
				insertReports(t, r, db, server, now.Add(-6*time.Hour), now, 20)
				if err := off.RunCalcDropMatrixJob(ctx, server); err != nil {
					t.Fatal(err)
				}
			}
			compare(step)
		},
		"admin recalculation": func(step string) {
			server := testServers[r.Intn(len(testServers))]
			now := util.Now()
			for _, date := range []time.Time{testBaseTime.Add(time.Duration(r.Intn(4*24)) * time.Hour), now, now.Add(-24 * time.Hour)} {
				dayStart := time.UnixMilli(util.GetDayStartTime(&date, server))
				insertReports(t, r, db, server, dayStart, dayStart.Add(24*time.Hour), 10)
				if err := off.UpdateDropMatrixByGivenDate(ctx, server, &date); err != nil {
					t.Fatal(err)
				}
				compare(step + " of " + date.Format(time.RFC3339))
			}
		},
		"repair": func(step string) {
			server := testServers[r.Intn(len(testServers))]
			now := util.Now()
			old := util.GetDayNum(&now, server) - 2
			mustExec(t, db, "UPDATE drop_matrix_elements SET times = times + 1, quantity = quantity + 1 WHERE element_id = ?", randomElement(server, old))
			compare(step + " update")
			mustExec(t, db, "DELETE FROM drop_matrix_elements WHERE element_id = ?", randomElement(server, old))
			compare(step + " delete")
		},
		"truncate": func(step string) {
			mustExec(t, db, "CREATE TABLE saved_elements AS SELECT * FROM drop_matrix_elements")
			mustExec(t, db, "TRUNCATE drop_matrix_elements")
			compare(step + " empty")
			mustExec(t, db, "INSERT INTO drop_matrix_elements SELECT * FROM saved_elements")
			mustExec(t, db, "DROP TABLE saved_elements")
			compare(step + " reloaded")
		},
		"trigger disabled": func(step string) {
			server := testServers[r.Intn(len(testServers))]
			now := util.Now()
			mustExec(t, db, "ALTER TABLE drop_matrix_elements DISABLE TRIGGER matrix_element_day_versions_upd")
			mustExec(t, db, "UPDATE drop_matrix_elements SET quantity = quantity + 1 WHERE element_id = ?", randomElement(server, util.GetDayNum(&now, server)-2))
			compare(step + " while disabled")
			mustExec(t, db, "UPDATE drop_matrix_elements SET quantity = quantity + 1 WHERE element_id = ?", randomElement(server, util.GetDayNum(&now, server)-2))
			mustExec(t, db, "ALTER TABLE drop_matrix_elements ENABLE ALWAYS TRIGGER matrix_element_day_versions_upd")
			compare(step + " enabled again")
		},
		"range end": func(step string) {
			server := testServers[r.Intn(len(testServers))]
			mustExec(t, db, "UPDATE time_ranges SET end_time = end_time + interval '1 hour' WHERE range_id = ?",
				randomId("SELECT DISTINCT range_id FROM drop_infos WHERE server = ? AND item_id IS NOT NULL ORDER BY range_id", server))
			compare(step)
		},
		"accumulable": func(step string) {
			server := testServers[r.Intn(len(testServers))]
			mustExec(t, db, "UPDATE drop_infos SET accumulable = NOT accumulable WHERE range_id = ? AND server = ?",
				randomId("SELECT DISTINCT range_id FROM drop_infos WHERE server = ? AND item_id IS NOT NULL ORDER BY range_id", server), server)
			compare(step)
		},
		"time range added": func(step string) {
			server := testServers[r.Intn(len(testServers))]
			start := testBaseTime.Add(time.Duration(r.Intn(4*24)) * time.Hour)
			var rangeId int64
			if err := db.NewRaw("INSERT INTO time_ranges (name, start_time, end_time, server) VALUES ('added', ?, ?, ?) RETURNING range_id",
				start, start.Add(time.Duration(12+r.Intn(48))*time.Hour), server).Scan(ctx, &rangeId); err != nil {
				t.Fatal(err)
			}
			stageId := 1 + r.Intn(6)
			for itemId := 10; itemId <= 15; itemId++ {
				if r.Intn(2) == 0 {
					mustExec(t, db, "INSERT INTO drop_infos (server, stage_id, item_id, drop_type, range_id, accumulable) VALUES (?, ?, ?, 'REGULAR', ?, true)",
						server, stageId, itemId, rangeId)
				}
			}
			compare(step)
		},
		"target added": func(step string) {
			server := testServers[r.Intn(len(testServers))]
			rangeId := randomId("SELECT DISTINCT range_id FROM drop_infos WHERE server = ? AND item_id IS NOT NULL ORDER BY range_id", server)
			mustExec(t, db, "INSERT INTO drop_infos (server, stage_id, item_id, drop_type, range_id, accumulable) VALUES (?, ?, ?, 'REGULAR', ?, true)",
				server, 1+r.Intn(6), 10+r.Intn(6), rangeId)
			compare(step)
		},
		"target removed": func(step string) {
			server := testServers[r.Intn(len(testServers))]
			mustExec(t, db, "DELETE FROM drop_infos WHERE drop_id = ?",
				randomId("SELECT drop_id FROM drop_infos WHERE server = ? AND item_id IS NOT NULL ORDER BY drop_id", server))
			compare(step)
		},
		"source categories": func(step string) {
			categories := make([]string, 0)
			for len(categories) == 0 {
				for _, sourceCategory := range allSourceCategories {
					if r.Intn(2) == 0 {
						categories = append(categories, sourceCategory)
					}
				}
			}
			off.Config.MatrixWorkerSourceCategories, on.Config.MatrixWorkerSourceCategories = categories, categories
			compare(step + " " + fmt.Sprint(categories))
		},
		"max age": func(step string) {
			on.Config.GlobalMatrixBaseMaxAge = time.Nanosecond
			compare(step)
			on.Config.GlobalMatrixBaseMaxAge = 1000 * time.Hour
		},
	}
	names := make([]string, 0, len(ops))
	for name := range ops {
		names = append(names, name)
	}
	// every operation three times, in a random order (map order is random: sort first, for a reproducible sequence)
	sort.Strings(names)
	sequence := make([]string, 0, 3*len(names))
	for i := 0; i < 3; i++ {
		sequence = append(sequence, names...)
	}
	r.Shuffle(len(sequence), func(i, j int) { sequence[i], sequence[j] = sequence[j], sequence[i] })
	for i, name := range sequence {
		ops[name](fmt.Sprintf("step %d (%s)", i, name))
	}
	requireCompared(t, compared, 100)

	paths := globalMatrixPathCounts()
	for _, pr := range []string{
		"incremental none", "rebuild no_base", "rebuild fingerprint", "rebuild max_age", "full triggers_missing",
	} {
		if paths[pr] == pathsBefore[pr] {
			t.Errorf("the calculations never took the %s path: the sequence does not exercise it (%v)", pr, paths)
		}
	}
	if globalMatrixVerifyMismatchCount() != mismatchesBefore {
		t.Error("a verification counted a mismatch")
	}
	t.Logf("paths: %v", paths)
}

// A batch of the worker that rewrites today while an incremental calculation reads: the calculation reads one
// snapshot, its fingerprint, base extension and verification alike, and gets the matrices of before the batch; the
// next one those of after.
func TestGlobalDropMatrixIncrementalSnapshot(t *testing.T) {
	db, off, _ := newGlobalMatrixTestData(t)
	ctx := context.Background()
	r := rand.New(rand.NewSource(1))
	on := incrementalDropMatrix(off, appconfig.GlobalMatrixIncrementalOn)
	on.Config.GlobalMatrixVerifyEvery = 1
	flushCaches(t)
	server := "CN"
	categories := allSourceCategories

	// a base, then the matrices without bases before the batch
	if _, err := on.calcGlobalDropMatrices(ctx, server, categories); err != nil {
		t.Fatal(err)
	}
	before, err := off.calcGlobalDropMatrices(ctx, server, categories)
	if err != nil {
		t.Fatal(err)
	}
	mismatches := globalMatrixVerifyMismatchCount()
	incremental := testutil.ToFloat64(observability.GlobalMatrixRefreshPaths.WithLabelValues(globalMatrixKindDrop, server, constant.SourceCategoryAll, globalMatrixPathIncremental, globalMatrixReasonNone))

	// the calculation pauses once it read the triggers, the first statement of its snapshot
	pause := &pauseAfterReading{table: "matrix_element_day_versions", read: make(chan struct{}), release: make(chan struct{})}
	db.AddQueryHook(pause)
	type calculated struct {
		results map[string]*model.DropMatrixQueryResult
		err     error
	}
	done := make(chan calculated, 1)
	go func() {
		results, err := on.calcGlobalDropMatrices(context.WithValue(ctx, pauseAfterReadingKey{}, pause), server, categories)
		done <- calculated{results, err}
	}()
	select {
	case <-pause.read:
	case <-time.After(10 * time.Second):
		t.Fatal("the calculation never read the triggers")
	}
	now := util.Now()
	insertReports(t, r, db, server, time.UnixMilli(util.GetDayStartTime(&now, server)), now, 200)
	if err := off.RunCalcDropMatrixJob(ctx, server); err != nil {
		t.Fatal(err)
	}
	flushCachesKeepingBases(t)
	after, err := off.calcGlobalDropMatrices(ctx, server, categories)
	if err != nil {
		t.Fatal(err)
	}
	close(pause.release)
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}

	changed := false
	for _, sourceCategory := range categories {
		assertSameLines(t, sourceCategory+" during the batch", globalDropMatrixLines(before[sourceCategory]), globalDropMatrixLines(got.results[sourceCategory]))
		changed = changed || fmt.Sprint(globalDropMatrixLines(before[sourceCategory])) != fmt.Sprint(globalDropMatrixLines(after[sourceCategory]))
	}
	if !changed {
		t.Fatal("the batch did not change the matrices: the test does not exercise the snapshot")
	}
	if globalMatrixVerifyMismatchCount() != mismatches {
		t.Fatal("the verification in the snapshot of the calculation found a mismatch")
	}
	if n := testutil.ToFloat64(observability.GlobalMatrixRefreshPaths.WithLabelValues(globalMatrixKindDrop, server, constant.SourceCategoryAll, globalMatrixPathIncremental, globalMatrixReasonNone)); n != incremental+1 {
		t.Fatalf("the calculation during the batch did not extend the base (%v incremental calculations, want %v)", n, incremental+1)
	}

	next, err := on.calcGlobalDropMatrices(ctx, server, categories)
	if err != nil {
		t.Fatal(err)
	}
	for _, sourceCategory := range categories {
		assertSameLines(t, sourceCategory+" after the batch", globalDropMatrixLines(after[sourceCategory]), globalDropMatrixLines(next[sourceCategory]))
	}
	if globalMatrixVerifyMismatchCount() != mismatches {
		t.Fatal("a verification found a mismatch")
	}
}

// Without the migration, or with the day versions not readable, the incremental mode aggregates every day.
func TestGlobalDropMatrixIncrementalWithoutDayVersions(t *testing.T) {
	db, off, _ := newGlobalMatrixTestData(t)
	ctx := context.Background()
	on := incrementalDropMatrix(off, appconfig.GlobalMatrixIncrementalVerify)
	flushCaches(t)
	want, err := off.calcGlobalDropMatrices(ctx, "CN", allSourceCategories)
	if err != nil {
		t.Fatal(err)
	}
	full := func() float64 {
		return testutil.ToFloat64(observability.GlobalMatrixRefreshPaths.WithLabelValues(globalMatrixKindDrop, "CN", constant.SourceCategoryAll, globalMatrixPathFull, globalMatrixReasonTriggersMissing))
	}
	check := func(what string) {
		t.Helper()
		before := full()
		got, err := on.calcGlobalDropMatrices(ctx, "CN", allSourceCategories)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		for _, sourceCategory := range allSourceCategories {
			assertSameLines(t, what+" "+sourceCategory, globalDropMatrixLines(want[sourceCategory]), globalDropMatrixLines(got[sourceCategory]))
		}
		if full() != before+1 {
			t.Fatalf("%s: did not aggregate every day without a base", what)
		}
		if cache.GlobalDropMatrixBase.Has(globalDropMatrixBaseKey("CN", constant.SourceCategoryAll)) {
			t.Fatalf("%s: a base is cached", what)
		}
	}

	// the day versions not readable (missing, as SELECT not granted would)
	mustExec(t, db, "ALTER TABLE matrix_element_day_versions RENAME TO matrix_element_day_versions_away")
	check("without the day versions")
	mustExec(t, db, "ALTER TABLE matrix_element_day_versions_away RENAME TO matrix_element_day_versions")

	// the migration reverted
	down, err := os.ReadFile("../../scripts/migrations/20261025_matrix_element_day_versions_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, string(down))
	check("without the migration")
}
