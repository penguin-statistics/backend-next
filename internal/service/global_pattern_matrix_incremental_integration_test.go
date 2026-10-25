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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/util"
)

// incrementalPatternMatrix returns a copy of off with its own configuration, calculating the global pattern matrices
// incrementally.
func incrementalPatternMatrix(off *PatternMatrix, mode appconfig.GlobalMatrixIncrementalMode) *PatternMatrix {
	conf := *off.Config
	conf.GlobalMatrixIncremental = mode
	conf.GlobalMatrixBaseMaxAge = 1000 * time.Hour
	conf.GlobalMatrixMaxChangedStages = 2
	return &PatternMatrix{
		Config: &conf, TimeRangeService: off.TimeRangeService, DropReportService: off.DropReportService, DropInfoService: off.DropInfoService,
		PatternMatrixElementService: off.PatternMatrixElementService, DropPatternElementService: off.DropPatternElementService,
		StageService: off.StageService, ItemService: off.ItemService,
	}
}

// refreshedPatternMatrices refreshes the global pattern matrices of every server with s, and returns them as cached,
// with all and with the top patterns, of every source category it refreshes, and as calculated (raw).
func refreshedPatternMatrices(t *testing.T, s *PatternMatrix) map[string]string {
	t.Helper()
	ctx := context.Background()
	results := make(map[string]string)
	for _, server := range testServers {
		if err := s.RefreshGlobalPatternMatrixCaches(ctx, server); err != nil {
			t.Fatal(err)
		}
		for _, sourceCategory := range s.Config.MatrixWorkerSourceCategories {
			for _, showAllPatterns := range []bool{true, false} {
				var shim modelv2.PatternMatrixQueryResult
				key := server + constant.CacheSep + sourceCategory + constant.CacheSep + strconv.FormatBool(showAllPatterns)
				if err := cache.ShimGlobalPatternMatrix.Get(key, &shim); err != nil {
					t.Fatal(err)
				}
				results[server+" "+sourceCategory+" shim "+strconv.FormatBool(showAllPatterns)] = canonicalJSON(t, shim.PatternMatrix)
			}
		}
		raw, err := s.calcGlobalPatternMatrices(ctx, server, s.Config.MatrixWorkerSourceCategories, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, sourceCategory := range s.Config.MatrixWorkerSourceCategories {
			results[server+" "+sourceCategory+" raw"] = fmt.Sprint(globalPatternMatrixLines(raw[sourceCategory]))
		}
	}
	return results
}

// globalMatrixChangedStageCount sums the changed stages the incremental calculations of the global matrices of kind
// of the test servers found.
func globalMatrixChangedStageCount(t *testing.T, kind string) float64 {
	t.Helper()
	var n float64
	for _, server := range testServers {
		var m dto.Metric
		if err := observability.GlobalMatrixChangedStages.WithLabelValues(kind, server).(prometheus.Metric).Write(&m); err != nil {
			t.Fatal(err)
		}
		n += m.GetHistogram().GetSampleSum()
	}
	return n
}

// TestGlobalPatternMatrixIncrementalMatchesOff is TestGlobalDropMatrixIncrementalMatchesOff for the global pattern
// matrices: random sequences of the worker's batches as the clock goes on, admin recalculations of old days, today
// and yesterday, SQL repairs of old elements, a TRUNCATE and reload, writes while a trigger is disabled, edits of the
// time ranges and drop infos that change the latest time range of a stage, changes of the source categories of the
// worker and an expired base. After each, the incremental global pattern matrices, raw and shim, are those
// calculated without bases.
func TestGlobalPatternMatrixIncrementalMatchesOff(t *testing.T) {
	db, _, off := newGlobalMatrixTestData(t)
	ctx := context.Background()
	r := rand.New(rand.NewSource(861))
	on := incrementalPatternMatrix(off, appconfig.GlobalMatrixIncrementalOn)
	flushCaches(t)
	pathsBefore := globalMatrixPathCounts(globalMatrixKindPattern)
	mismatchesBefore := globalMatrixVerifyMismatchCount(globalMatrixKindPattern)
	changedStagesBefore := globalMatrixChangedStageCount(t, globalMatrixKindPattern)

	compared := 0
	compare := func(step string) {
		t.Helper()
		flushCachesKeepingBases(t)
		want := refreshedPatternMatrices(t, off)
		got := refreshedPatternMatrices(t, on)
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
		w, err := off.calcGlobalPatternMatrix(ctx, server, sourceCategory)
		if err != nil {
			t.Fatal(err)
		}
		g, err := on.calcGlobalPatternMatrix(ctx, server, sourceCategory)
		if err != nil {
			t.Fatal(err)
		}
		assertSameLines(t, step+" request "+server+" "+sourceCategory, globalPatternMatrixLines(w), globalPatternMatrixLines(g))
	}
	setNow := func(now time.Time) { util.Now = func() time.Time { return now } }
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
	randomElement := func(server string, beforeDayNum int) int64 {
		return randomId("SELECT element_id FROM pattern_matrix_elements WHERE server = ? AND day_num < ? AND quantity > 0 ORDER BY element_id",
			server, beforeDayNum)
	}
	// the time ranges of the drop infos with an item of server, which the latest time ranges of the stages are of
	rangeOfItems := "SELECT DISTINCT range_id FROM drop_infos WHERE server = ? AND item_id IS NOT NULL ORDER BY range_id"

	compare("start")
	ops := map[string]func(step string){
		"batch": func(step string) {
			now := util.Now()
			for _, server := range testServers {
				insertReports(t, r, db, server, time.UnixMilli(util.GetDayStartTime(&now, server)), now, 30)
				if err := off.RunCalcPatternMatrixJob(ctx, server); err != nil {
					t.Fatal(err)
				}
			}
			compare(step)
		},
		"next day": func(step string) {
			setNow(util.Now().Add(time.Duration(6+r.Intn(30)) * time.Hour))
			now := util.Now()
			for _, server := range testServers {
				insertReports(t, r, db, server, now.Add(-6*time.Hour), now, 30)
				if err := off.RunCalcPatternMatrixJob(ctx, server); err != nil {
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
				insertReports(t, r, db, server, dayStart, dayStart.Add(24*time.Hour), 15)
				if err := off.UpdatePatternMatrixByGivenDate(ctx, server, &date); err != nil {
					t.Fatal(err)
				}
				compare(step + " of " + date.Format(time.RFC3339))
			}
		},
		"repair": func(step string) {
			server := testServers[r.Intn(len(testServers))]
			now := util.Now()
			// a day of the bases (before yesterday): the elements of the latest time ranges only, of fewer days
			// than the drop matrix elements
			old := util.GetDayNum(&now, server) - 1
			mustExec(t, db, "UPDATE pattern_matrix_elements SET times = times + 1, quantity = quantity + 1 WHERE element_id = ?", randomElement(server, old))
			compare(step + " update")
			mustExec(t, db, "DELETE FROM pattern_matrix_elements WHERE element_id = ?", randomElement(server, old))
			compare(step + " delete")
		},
		"truncate": func(step string) {
			mustExec(t, db, "CREATE TABLE saved_elements AS SELECT * FROM pattern_matrix_elements")
			mustExec(t, db, "TRUNCATE pattern_matrix_elements")
			compare(step + " empty")
			mustExec(t, db, "INSERT INTO pattern_matrix_elements SELECT * FROM saved_elements")
			mustExec(t, db, "DROP TABLE saved_elements")
			compare(step + " reloaded")
		},
		"trigger disabled": func(step string) {
			server := testServers[r.Intn(len(testServers))]
			now := util.Now()
			mustExec(t, db, "ALTER TABLE pattern_matrix_elements DISABLE TRIGGER matrix_element_day_versions_upd")
			mustExec(t, db, "UPDATE pattern_matrix_elements SET quantity = quantity + 1 WHERE element_id = ?", randomElement(server, util.GetDayNum(&now, server)-1))
			compare(step + " while disabled")
			mustExec(t, db, "UPDATE pattern_matrix_elements SET quantity = quantity + 1 WHERE element_id = ?", randomElement(server, util.GetDayNum(&now, server)-1))
			mustExec(t, db, "ALTER TABLE pattern_matrix_elements ENABLE ALWAYS TRIGGER matrix_element_day_versions_upd")
			compare(step + " enabled again")
		},
		"range end": func(step string) {
			server := testServers[r.Intn(len(testServers))]
			mustExec(t, db, "UPDATE time_ranges SET end_time = end_time + interval '1 hour' WHERE range_id = ?", randomId(rangeOfItems, server))
			compare(step)
		},
		"time range added": func(step string) {
			server := testServers[r.Intn(len(testServers))]
			start := testBaseTime.Add(time.Duration(r.Intn(5*24)) * time.Hour)
			var rangeId int64
			if err := db.NewRaw("INSERT INTO time_ranges (name, start_time, end_time, server) VALUES ('added', ?, ?, ?) RETURNING range_id",
				start, start.Add(time.Duration(12+r.Intn(48))*time.Hour), server).Scan(ctx, &rangeId); err != nil {
				t.Fatal(err)
			}
			stageId := 1 + r.Intn(6)
			mustExec(t, db, "INSERT INTO drop_infos (server, stage_id, item_id, drop_type, range_id, accumulable) VALUES (?, ?, ?, 'REGULAR', ?, true)",
				server, stageId, 10+r.Intn(6), rangeId)
			compare(step)
		},
		"target added": func(step string) {
			server := testServers[r.Intn(len(testServers))]
			mustExec(t, db, "INSERT INTO drop_infos (server, stage_id, item_id, drop_type, range_id, accumulable) VALUES (?, ?, ?, 'REGULAR', ?, true)",
				server, 1+r.Intn(6), 10+r.Intn(6), randomId(rangeOfItems, server))
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

	paths := globalMatrixPathCounts(globalMatrixKindPattern)
	for _, pr := range []string{
		"incremental none", "rebuild no_base", "rebuild fingerprint", "rebuild max_age", "full triggers_missing",
	} {
		if paths[pr] == pathsBefore[pr] {
			t.Errorf("the calculations never took the %s path: the sequence does not exercise it (%v)", pr, paths)
		}
	}
	changedStages := globalMatrixChangedStageCount(t, globalMatrixKindPattern) - changedStagesBefore
	if changedStages == 0 {
		t.Error("no incremental calculation recomputed a changed stage: the sequence does not exercise it")
	}
	if globalMatrixVerifyMismatchCount(globalMatrixKindPattern) != mismatchesBefore {
		t.Error("a verification counted a mismatch")
	}
	t.Logf("paths: %v; changed stages recomputed: %v", paths, changedStages)
}

// A batch of the worker that rewrites today's pattern matrix elements while an incremental calculation reads: the
// calculation reads one snapshot, its fingerprint, base extension and verification alike, and gets the matrices of
// before the batch; the next one those of after.
func TestGlobalPatternMatrixIncrementalSnapshot(t *testing.T) {
	db, _, off := newGlobalMatrixTestData(t)
	ctx := context.Background()
	r := rand.New(rand.NewSource(1))
	on := incrementalPatternMatrix(off, appconfig.GlobalMatrixIncrementalOn)
	on.Config.GlobalMatrixVerifyEvery = 1
	flushCaches(t)
	server := "CN"
	categories := allSourceCategories

	// a base, then the matrices without bases before the batch
	if _, err := on.calcGlobalPatternMatrices(ctx, server, categories, false); err != nil {
		t.Fatal(err)
	}
	before, err := off.calcGlobalPatternMatrices(ctx, server, categories, false)
	if err != nil {
		t.Fatal(err)
	}
	mismatches := globalMatrixVerifyMismatchCount(globalMatrixKindPattern)
	incremental := func() float64 {
		return testutil.ToFloat64(observability.GlobalMatrixRefreshPaths.WithLabelValues(globalMatrixKindPattern, server, constant.SourceCategoryAll, globalMatrixPathIncremental, globalMatrixReasonNone))
	}
	incrementalBefore := incremental()

	// the calculation pauses once it read the triggers, the first statement of its snapshot
	pause := &pauseAfterReading{table: "matrix_element_day_versions", read: make(chan struct{}), release: make(chan struct{})}
	db.AddQueryHook(pause)
	type calculated struct {
		results map[string]*model.PatternMatrixQueryResult
		err     error
	}
	done := make(chan calculated, 1)
	go func() {
		results, err := on.calcGlobalPatternMatrices(context.WithValue(ctx, pauseAfterReadingKey{}, pause), server, categories, false)
		done <- calculated{results, err}
	}()
	select {
	case <-pause.read:
	case <-time.After(10 * time.Second):
		t.Fatal("the calculation never read the triggers")
	}
	now := util.Now()
	insertReports(t, r, db, server, time.UnixMilli(util.GetDayStartTime(&now, server)), now, 300)
	if err := off.RunCalcPatternMatrixJob(ctx, server); err != nil {
		t.Fatal(err)
	}
	flushCachesKeepingBases(t)
	after, err := off.calcGlobalPatternMatrices(ctx, server, categories, false)
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
		assertSameLines(t, sourceCategory+" during the batch", globalPatternMatrixLines(before[sourceCategory]), globalPatternMatrixLines(got.results[sourceCategory]))
		changed = changed || fmt.Sprint(globalPatternMatrixLines(before[sourceCategory])) != fmt.Sprint(globalPatternMatrixLines(after[sourceCategory]))
	}
	if !changed {
		t.Fatal("the batch did not change the matrices: the test does not exercise the snapshot")
	}
	if globalMatrixVerifyMismatchCount(globalMatrixKindPattern) != mismatches {
		t.Fatal("the verification in the snapshot of the calculation found a mismatch")
	}
	if n := incremental(); n != incrementalBefore+1 {
		t.Fatalf("the calculation during the batch did not extend the base (%v incremental calculations, want %v)", n, incrementalBefore+1)
	}

	next, err := on.calcGlobalPatternMatrices(ctx, server, categories, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, sourceCategory := range categories {
		assertSameLines(t, sourceCategory+" after the batch", globalPatternMatrixLines(after[sourceCategory]), globalPatternMatrixLines(next[sourceCategory]))
	}
	if globalMatrixVerifyMismatchCount(globalMatrixKindPattern) != mismatches {
		t.Fatal("a verification found a mismatch")
	}
}

// A refresh after a batch while a request, without a base, rebuilds one from a snapshot of before the batch: the
// refresh caches the matrices of after the batch instead of taking those of the request's rebuild, and the request
// does not cache its own over them.
func TestGlobalPatternMatrixIncrementalRefreshDuringRequestRebuild(t *testing.T) {
	db, _, off := newGlobalMatrixTestData(t)
	ctx := context.Background()
	r := rand.New(rand.NewSource(2))
	on := incrementalPatternMatrix(off, appconfig.GlobalMatrixIncrementalOn)
	flushCaches(t)
	server := "CN"
	key := server + constant.CacheSep + constant.SourceCategoryAll + constant.CacheSep + "true"
	// the shim matrix with all patterns, as a request calculates it, without caching it
	shim := func(s *PatternMatrix) string {
		raw, err := s.calcGlobalPatternMatrix(ctx, server, constant.SourceCategoryAll)
		if err != nil {
			t.Fatal(err)
		}
		result, err := s.applyShimForPatternMatrixQuery(ctx, raw)
		if err != nil {
			t.Fatal(err)
		}
		return canonicalJSON(t, result.PatternMatrix)
	}
	before := shim(off)

	// the request pauses once it read the triggers, the first statement of the snapshot of its rebuild
	pause := &pauseAfterReading{table: "matrix_element_day_versions", read: make(chan struct{}), release: make(chan struct{})}
	db.AddQueryHook(pause)
	released := false
	release := func() {
		if !released {
			released = true
			close(pause.release)
		}
	}
	defer release()
	type requested struct {
		result *modelv2.PatternMatrixQueryResult
		err    error
	}
	requestDone := make(chan requested, 1)
	go func() {
		result, err := on.GetShimPatternMatrix(context.WithValue(ctx, pauseAfterReadingKey{}, pause), server, null.Int{}, constant.SourceCategoryAll, true)
		requestDone <- requested{result, err}
	}()
	select {
	case <-pause.read:
	case <-time.After(10 * time.Second):
		t.Fatal("the request never read the triggers")
	}
	now := util.Now()
	insertReports(t, r, db, server, time.UnixMilli(util.GetDayStartTime(&now, server)), now, 300)
	if err := off.RunCalcPatternMatrixJob(ctx, server); err != nil {
		t.Fatal(err)
	}
	after := shim(off)
	if before == after {
		t.Fatal("the batch did not change the matrix: the test does not exercise the snapshot")
	}

	refreshDone := make(chan error, 1)
	go func() { refreshDone <- on.RefreshGlobalPatternMatrixCaches(ctx, server) }()
	var err error
	select {
	case err = <-refreshDone:
	case <-time.After(10 * time.Second):
		t.Error("the refresh waits for the rebuild of the request")
		release()
		err = <-refreshDone
	}
	if err != nil {
		t.Fatal(err)
	}
	var cached modelv2.PatternMatrixQueryResult
	if err := cache.ShimGlobalPatternMatrix.Get(key, &cached); err != nil {
		t.Fatal(err)
	}
	if got := canonicalJSON(t, cached.PatternMatrix); got != after {
		t.Fatalf("cached by the refresh: other than after the batch\ngot  %.300s\nwant %.300s", got, after)
	}

	release()
	got := <-requestDone
	if got.err != nil {
		t.Fatal(got.err)
	}
	if g := canonicalJSON(t, got.result.PatternMatrix); g != after {
		t.Fatalf("returned to the request: other than after the batch\ngot  %.300s\nwant %.300s", g, after)
	}
	if err := cache.ShimGlobalPatternMatrix.Get(key, &cached); err != nil {
		t.Fatal(err)
	}
	if g := canonicalJSON(t, cached.PatternMatrix); g != after {
		t.Fatalf("cached after the request: other than after the batch\ngot  %.300s\nwant %.300s", g, after)
	}
}

// Without the migration, or with the day versions not readable, the incremental mode aggregates every day.
func TestGlobalPatternMatrixIncrementalWithoutDayVersions(t *testing.T) {
	db, _, off := newGlobalMatrixTestData(t)
	ctx := context.Background()
	on := incrementalPatternMatrix(off, appconfig.GlobalMatrixIncrementalVerify)
	flushCaches(t)
	want, err := off.calcGlobalPatternMatrices(ctx, "CN", allSourceCategories, false)
	if err != nil {
		t.Fatal(err)
	}
	full := func() float64 {
		return testutil.ToFloat64(observability.GlobalMatrixRefreshPaths.WithLabelValues(globalMatrixKindPattern, "CN", constant.SourceCategoryAll, globalMatrixPathFull, globalMatrixReasonTriggersMissing))
	}
	check := func(what string) {
		t.Helper()
		before := full()
		got, err := on.calcGlobalPatternMatrices(ctx, "CN", allSourceCategories, false)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		for _, sourceCategory := range allSourceCategories {
			assertSameLines(t, what+" "+sourceCategory, globalPatternMatrixLines(want[sourceCategory]), globalPatternMatrixLines(got[sourceCategory]))
		}
		if full() != before+1 {
			t.Fatalf("%s: did not aggregate every day without a base", what)
		}
		if cache.GlobalPatternMatrixBase.Has(globalPatternMatrixBaseKey("CN", constant.SourceCategoryAll)) {
			t.Fatalf("%s: a base is cached", what)
		}
	}

	// the day versions not readable (missing, as SELECT not granted would)
	mustExec(t, db, "ALTER TABLE matrix_element_day_versions RENAME TO matrix_element_day_versions_away")
	check("without the day versions")
	mustExec(t, db, "ALTER TABLE matrix_element_day_versions_away RENAME TO matrix_element_day_versions")

	// a trigger of the pattern matrix elements disabled, those of the drop matrix elements firing
	mustExec(t, db, "ALTER TABLE pattern_matrix_elements DISABLE TRIGGER matrix_element_day_versions_ins")
	check("with a trigger disabled")
	mustExec(t, db, "ALTER TABLE pattern_matrix_elements ENABLE ALWAYS TRIGGER matrix_element_day_versions_ins")

	// the migration reverted
	down, err := os.ReadFile("../../scripts/migrations/20261025_matrix_element_day_versions_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, string(down))
	check("without the migration")
}
