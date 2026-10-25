package service

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/util"
)

// fakePatternElement is a pattern matrix element of the fake store; its time range is in seconds.
type fakePatternElement struct {
	sourceCategory     string
	stageId, patternId int
	dayNum             int
	start, end         int64
	times, quantity    int
}

// fakeGlobalPatternMatrixStore is fakeGlobalDropMatrixStore for the pattern matrix elements.
type fakeGlobalPatternMatrixStore struct {
	mu          sync.Mutex
	elements    []fakePatternElement
	calls       []fakeDropMatrixCall
	versions    map[int]int64 // day → version, of the server of the test
	nextVersion int64
	identity    string
	missing     string
	// gate, if set, blocks the partials over every day until it is closed, after closing started once; once gated
	// blocks only the first of them
	gate      chan struct{}
	started   chan struct{}
	once      bool
	startOnce sync.Once
}

type fakePatternSnapshot struct {
	elements []fakePatternElement
	versions map[int]int64
}

type fakePatternSnapshotKey struct{}

// read returns the elements and versions of the snapshot of ctx, or, without one, the current ones. f.mu is held.
func (f *fakeGlobalPatternMatrixStore) read(ctx context.Context) ([]fakePatternElement, map[int]int64) {
	if snapshot, ok := ctx.Value(fakePatternSnapshotKey{}).(*fakePatternSnapshot); ok {
		return snapshot.elements, snapshot.versions
	}
	return f.elements, f.versions
}

// replaceDay replaces the elements of a day, as a writer does.
func (f *fakeGlobalPatternMatrixStore) replaceDay(dayNum int, elements []fakePatternElement) {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.elements[:0:0]
	for _, el := range f.elements {
		if el.dayNum != dayNum {
			kept = append(kept, el)
		}
	}
	f.elements = append(kept, elements...)
	f.nextVersion++
	f.versions[dayNum] = f.nextVersion
}

func (f *fakeGlobalPatternMatrixStore) RunInSnapshotTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error {
	f.mu.Lock()
	// replaceDay replaces f.elements, never modifies it
	snapshot := &fakePatternSnapshot{elements: f.elements, versions: make(map[int]int64, len(f.versions))}
	for dayNum, version := range f.versions {
		snapshot.versions[dayNum] = version
	}
	f.mu.Unlock()
	return fn(context.WithValue(ctx, fakePatternSnapshotKey{}, snapshot), bun.Tx{})
}

// aggregate aggregates the elements of the targets as the statements of the repo do: per stage, the smallest times
// > 0 of each day summed, per stage and pattern, the quantities > 0 summed.
func (f *fakeGlobalPatternMatrixStore) aggregate(
	ctx context.Context, targets []*model.GlobalPatternMatrixTarget, sourceCategories []string, scope *model.GlobalMatrixScope,
) (before, after map[string]*model.GlobalPatternMatrixAggregates) {
	f.mu.Lock()
	defer f.mu.Unlock()
	elements, _ := f.read(ctx)
	before, after = make(map[string]*model.GlobalPatternMatrixAggregates), make(map[string]*model.GlobalPatternMatrixAggregates)
	for _, sourceCategory := range sourceCategories {
		for _, side := range []map[string]*model.GlobalPatternMatrixAggregates{before, after} {
			side[sourceCategory] = &model.GlobalPatternMatrixAggregates{TimesByStageID: make(map[int]int), QuantitiesByStageID: make(map[int]map[int]int)}
		}
	}
	type daily struct {
		sourceCategory string
		stageId        int
		before         bool
		dayNum         int
	}
	smallest := make(map[daily]int)
	for _, target := range targets {
		for _, el := range elements {
			if el.stageId != target.StageID || !containsString(sourceCategories, el.sourceCategory) ||
				el.start < target.TimeRange.StartTime.Unix() || el.end > target.TimeRange.EndTime.Unix() {
				continue
			}
			if scope != nil && scope.FromDayNum.Valid && int64(el.dayNum) < scope.FromDayNum.Int64 {
				continue
			}
			isBefore := scope != nil && el.dayNum < scope.SplitDayNum
			side := after
			if isBefore {
				side = before
			}
			if d := (daily{el.sourceCategory, el.stageId, isBefore, el.dayNum}); el.times > 0 {
				if times, ok := smallest[d]; !ok || el.times < times {
					smallest[d] = el.times
				}
			}
			if el.quantity > 0 {
				quantities := side[el.sourceCategory].QuantitiesByStageID
				if quantities[el.stageId] == nil {
					quantities[el.stageId] = make(map[int]int)
				}
				quantities[el.stageId][el.patternId] += el.quantity
			}
		}
	}
	for d, times := range smallest {
		side := after
		if d.before {
			side = before
		}
		side[d.sourceCategory].TimesByStageID[d.stageId] += times
	}
	return before, after
}

func (f *fakeGlobalPatternMatrixStore) record(call fakeDropMatrixCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeGlobalPatternMatrixStore) takeCalls() []fakeDropMatrixCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := f.calls
	f.calls = nil
	return calls
}

func (f *fakeGlobalPatternMatrixStore) CalcGlobalPatternMatrixAggregates(
	ctx context.Context, _ bun.IDB, _ string, targets []*model.GlobalPatternMatrixTarget, sourceCategories []string,
) (map[string]*model.GlobalPatternMatrixAggregates, error) {
	f.record(fakeDropMatrixCall{kind: "reference", targets: len(targets)})
	_, all := f.aggregate(ctx, targets, sourceCategories, nil)
	return all, nil
}

func (f *fakeGlobalPatternMatrixStore) CalcGlobalPatternMatrixPartials(
	ctx context.Context, _ bun.IDB, _ string, targets []*model.GlobalPatternMatrixTarget, sourceCategories []string, scope model.GlobalMatrixScope,
) (before, after map[string]*model.GlobalPatternMatrixAggregates, err error) {
	if len(targets) == 0 {
		before, after = f.aggregate(ctx, nil, sourceCategories, &scope)
		return before, after, nil
	}
	call := fakeDropMatrixCall{kind: "partials", scope: scope, targets: len(targets)}
	for _, target := range targets {
		call.stages = append(call.stages, target.StageID)
	}
	sort.Ints(call.stages)
	f.record(call)
	if f.gate != nil && !scope.FromDayNum.Valid && !scope.StageFirst {
		first := false
		f.startOnce.Do(func() {
			first = true
			close(f.started)
		})
		if first || !f.once {
			<-f.gate
		}
		// as a statement of a context canceled meanwhile
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
	}
	before, after = f.aggregate(ctx, targets, sourceCategories, &scope)
	return before, after, nil
}

func (f *fakeGlobalPatternMatrixStore) Triggers(context.Context, bun.IDB, string) (identity, missing string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.identity, f.missing, nil
}

func (f *fakeGlobalPatternMatrixStore) DayFingerprints(ctx context.Context, _ bun.IDB, _, _ string, bounds []int) (map[int]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, versions := f.read(ctx)
	return fakeDayFingerprints(versions, bounds), nil
}

// fakePatternTargets makes a target per stage 1..4, plus stage 9 without elements, over [start, end).
func fakePatternTargets(start, end time.Time) []*model.GlobalPatternMatrixTarget {
	targets := make([]*model.GlobalPatternMatrixTarget, 0)
	for _, stageId := range []int{1, 2, 3, 4, 9} {
		targets = append(targets, &model.GlobalPatternMatrixTarget{StageID: stageId, TimeRange: &model.TimeRange{StartTime: &start, EndTime: &end}})
	}
	return targets
}

// fakePatternDay makes random elements of a day of stages, two time ranges a stage, some outside the targets' time
// range, some with zero times or quantity.
func fakePatternDay(r *rand.Rand, dayNum int, start, end time.Time, stages ...int) []fakePatternElement {
	if len(stages) == 0 {
		stages = []int{1, 2, 3, 4}
	}
	elements := make([]fakePatternElement, 0)
	for _, sourceCategory := range []string{constant.SourceCategoryAll, constant.SourceCategoryManual} {
		for _, stageId := range stages {
			// the stage's elements of two time ranges of the day, with their own times: the smallest counts
			for part := 0; part < 2; part++ {
				times := r.Intn(6)
				elStart, elEnd := start.Unix()+int64(r.Intn(3600)), end.Unix()-int64(r.Intn(3600))
				if r.Intn(10) == 0 { // outside of the time range
					elEnd = end.Unix() + 1
				}
				for patternId := 1; patternId <= 4; patternId++ {
					if r.Intn(3) == 0 {
						continue
					}
					elements = append(elements, fakePatternElement{
						sourceCategory: sourceCategory, stageId: stageId, patternId: patternId, dayNum: dayNum,
						start: elStart, end: elEnd, times: times, quantity: r.Intn(times + 1),
					})
				}
			}
		}
	}
	return elements
}

func describeGlobalPatternMatrixAggregates(targets []*model.GlobalPatternMatrixTarget, aggregates *model.GlobalPatternMatrixAggregates) string {
	lines := make([]string, 0, len(targets))
	for _, target := range targets {
		lines = append(lines, fmt.Sprintf("%d %s", target.StageID, describeGlobalPatternMatrixStage(aggregates, target.StageID)))
	}
	sort.Strings(lines)
	return strings.Join(lines, "; ")
}

func describePatternBase(b model.GlobalPatternMatrixBase) string {
	stages := make([]int, 0)
	for stageId := range b.Partials.TimesByStageID {
		stages = append(stages, stageId)
	}
	for stageId := range b.Partials.QuantitiesByStageID {
		if _, ok := b.Partials.TimesByStageID[stageId]; !ok {
			stages = append(stages, stageId)
		}
	}
	sort.Ints(stages)
	lines := make([]string, 0, len(stages))
	for _, stageId := range stages {
		lines = append(lines, fmt.Sprintf("%d %s", stageId, describeGlobalPatternMatrixStage(&b.Partials, stageId)))
	}
	return fmt.Sprintf("%d %s %d %v", b.BeforeDayNum, b.Fingerprint, len(b.Targets), lines)
}

// TestGlobalPatternMatrixIncrementalDecisions is TestGlobalDropMatrixIncrementalDecisions for the global pattern
// matrices, whose targets are stages.
func TestGlobalPatternMatrixIncrementalDecisions(t *testing.T) {
	cache.Initialize()
	if err := cache.GlobalPatternMatrixBase.Flush(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		util.Now = time.Now
		_ = cache.GlobalPatternMatrixBase.Flush()
	})
	const server = "CN"
	ctx := context.Background()
	r := rand.New(rand.NewSource(8))
	now := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC) // 10:00 in CN
	util.Now = func() time.Time { return now }
	dayNum := func() int { return util.GetDayNum(&now, server) }

	start, end := now.Add(-30*24*time.Hour), now.Add(30*24*time.Hour)
	targets := fakePatternTargets(start, end)
	store := &fakeGlobalPatternMatrixStore{versions: make(map[int]int64), identity: "triggers-1"}
	for day := dayNum() - 9; day <= dayNum(); day++ {
		store.replaceDay(day, fakePatternDay(r, day, start, end))
	}
	conf := &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{
		GlobalMatrixIncremental: appconfig.GlobalMatrixIncrementalOn, GlobalMatrixBaseMaxAge: 1000 * time.Hour, GlobalMatrixMaxChangedStages: 1,
	}}
	s := &PatternMatrix{Config: conf, GlobalMatrixFingerprints: store, globalPatternMatrixStore: store}
	sourceCategories := []string{constant.SourceCategoryAll, constant.SourceCategoryManual}

	pathCount := func(sourceCategory, path, reason string) float64 {
		return testutil.ToFloat64(observability.GlobalMatrixRefreshPaths.WithLabelValues(globalMatrixKindPattern, server, sourceCategory, path, reason))
	}
	mismatchCount := func(sourceCategory string) float64 {
		return testutil.ToFloat64(observability.GlobalMatrixVerifyMismatches.WithLabelValues(globalMatrixKindPattern, server, sourceCategory))
	}
	base := func(sourceCategory string) (model.GlobalPatternMatrixBase, bool) {
		var b model.GlobalPatternMatrixBase
		err := cache.GlobalPatternMatrixBase.Get(globalPatternMatrixBaseKey(server, sourceCategory), &b)
		return b, err == nil
	}
	// calculate runs a calculation, checks its results against the reference and that every source category took
	// path for reason, and returns the aggregations it ran
	calculate := func(what, path, reason string) []fakeDropMatrixCall {
		t.Helper()
		before := make(map[string]float64)
		for _, sourceCategory := range sourceCategories {
			before[sourceCategory] = pathCount(sourceCategory, path, reason)
		}
		store.takeCalls()
		got, err := s.aggregateGlobalPatternMatricesIncrementally(ctx, server, targets, sourceCategories, false)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		calls := store.takeCalls()
		_, want := store.aggregate(context.Background(), targets, sourceCategories, nil)
		gotResults, err := assembleGlobalPatternMatrices(server, targets, got, sourceCategories)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		wantResults, err := assembleGlobalPatternMatrices(server, targets, want, sourceCategories)
		if err != nil {
			t.Fatalf("%s: the reference: %v", what, err)
		}
		for _, sourceCategory := range sourceCategories {
			if mismatches := diffGlobalPatternMatrixAggregates(targets, got[sourceCategory], want[sourceCategory]); len(mismatches) > 0 {
				t.Fatalf("%s %s: the aggregates differ from the reference: %v", what, sourceCategory, mismatches)
			}
			gotLines, wantLines := globalPatternMatrixLines(gotResults[sourceCategory]), globalPatternMatrixLines(wantResults[sourceCategory])
			if strings.Join(gotLines, "\n") != strings.Join(wantLines, "\n") {
				t.Fatalf("%s %s: the matrix differs from the reference\ngot  %v\nwant %v", what, sourceCategory, gotLines, wantLines)
			}
			if len(wantLines) == 0 {
				t.Fatalf("%s %s: the matrix is empty: the data does not exercise the calculation", what, sourceCategory)
			}
			if n := pathCount(sourceCategory, path, reason) - before[sourceCategory]; n != 1 {
				t.Fatalf("%s %s: took the %s path for %s %v times, want once; calls %+v", what, sourceCategory, path, reason, n, calls)
			}
		}
		return calls
	}
	expectBase := func(what string, beforeDayNum int) {
		t.Helper()
		for _, sourceCategory := range sourceCategories {
			b, ok := base(sourceCategory)
			if !ok {
				t.Fatalf("%s %s: no base cached", what, sourceCategory)
			}
			if b.BeforeDayNum != beforeDayNum {
				t.Fatalf("%s %s: the base is of the days before %d, want %d", what, sourceCategory, b.BeforeDayNum, beforeDayNum)
			}
			if len(b.Targets) != len(targets) {
				t.Fatalf("%s %s: the base has %d targets, want every one of the %d (also those without elements)", what, sourceCategory, len(b.Targets), len(targets))
			}
		}
	}
	expectCalls := func(what string, calls []fakeDropMatrixCall, want ...string) {
		t.Helper()
		got := make([]string, 0, len(calls))
		for _, call := range calls {
			got = append(got, describeFakeCall(call))
		}
		if strings.Join(got, "; ") != strings.Join(want, "; ") {
			t.Fatalf("%s: ran\n  %s\nwant\n  %s", what, strings.Join(got, "; "), strings.Join(want, "; "))
		}
	}
	twice := func(call string) []string { return []string{call, call} }
	all := len(targets)

	// no base: a rebuild per source category, over every day, the days before yesterday apart
	calls := calculate("no base", globalMatrixPathRebuild, globalMatrixReasonNoBase)
	d := dayNum() - 1
	expectCalls("no base", calls, twice(fmt.Sprintf("partials split %d targets %d", d, all))...)
	expectBase("no base", d)

	// nothing changed: the days since the base only, the target without elements is not recomputed
	calls = calculate("unchanged", globalMatrixPathIncremental, globalMatrixReasonNone)
	expectCalls("unchanged", calls, twice(fmt.Sprintf("partials split %d from %d targets %d", d, d, all))...)

	// today and yesterday rewritten (the worker): still the days since the base only; the old base is not modified
	oldBase, _ := base(constant.SourceCategoryAll)
	oldDescription := describePatternBase(oldBase)
	store.replaceDay(dayNum(), fakePatternDay(r, dayNum(), start, end))
	store.replaceDay(dayNum()-1, fakePatternDay(r, dayNum()-1, start, end))
	calculate("today and yesterday rewritten", globalMatrixPathIncremental, globalMatrixReasonNone)
	if describePatternBase(oldBase) != oldDescription {
		t.Fatal("extending a base modified it")
	}

	// the next day: the day settled since is added to the base
	now = now.Add(24 * time.Hour)
	store.replaceDay(dayNum(), fakePatternDay(r, dayNum(), start, end))
	calls = calculate("next day", globalMatrixPathIncremental, globalMatrixReasonNone)
	expectCalls("next day", calls, twice(fmt.Sprintf("partials split %d from %d targets %d", d+1, d, all))...)
	d++
	expectBase("next day", d)
	if describePatternBase(oldBase) != oldDescription {
		t.Fatal("extending a base modified it")
	}

	// a day of the base rewritten (an admin recalculation, a repair): rebuilt
	store.replaceDay(dayNum()-5, fakePatternDay(r, dayNum()-5, start, end))
	calculate("old day rewritten", globalMatrixPathRebuild, globalMatrixReasonFingerprint)
	expectBase("old day rewritten", d)

	// the triggers disabled and enabled again: another identity, the same versions; rebuilt
	store.identity = "triggers-2"
	calculate("trigger identity changed", globalMatrixPathRebuild, globalMatrixReasonFingerprint)

	// the time range of a stage changed: that stage is aggregated over every day, the others since the base
	changedStart := start.Add(time.Hour)
	setRange := func(stageId int, start time.Time) {
		for _, target := range targets {
			if target.StageID == stageId {
				target.TimeRange = &model.TimeRange{StartTime: &start, EndTime: &end}
			}
		}
	}
	setRange(2, changedStart)
	oldBase, _ = base(constant.SourceCategoryAll)
	oldDescription = describePatternBase(oldBase)
	calls = calculate("a stage changed", globalMatrixPathIncremental, globalMatrixReasonNone)
	expectCalls("a stage changed", calls,
		fmt.Sprintf("partials split %d from %d targets %d", d, d, all-1), fmt.Sprintf("partials split %d stage-first targets 1 stages [2]", d),
		fmt.Sprintf("partials split %d from %d targets %d", d, d, all-1), fmt.Sprintf("partials split %d stage-first targets 1 stages [2]", d))
	if describePatternBase(oldBase) != oldDescription {
		t.Fatal("recomputing a stage modified the base")
	}

	// a target stage added (with elements of an old day) and one removed: rebuilt for the old day; then the other
	// way round: the stage added back is recomputed, the stage removed is left out of the base
	targets = append(removePatternTarget(targets, 4), &model.GlobalPatternMatrixTarget{StageID: 5, TimeRange: &model.TimeRange{StartTime: &start, EndTime: &end}})
	store.replaceDay(dayNum()-3, fakePatternDay(r, dayNum()-3, start, end, 1, 2, 3, 4, 5))
	calculate("old day rewritten, again", globalMatrixPathRebuild, globalMatrixReasonFingerprint)
	if b, _ := base(constant.SourceCategoryAll); b.Partials.TimesByStageID[5] == 0 {
		t.Fatal("the stage added has no times in the base: the test does not exercise its removal")
	}
	targets = append(removePatternTarget(targets, 5), &model.GlobalPatternMatrixTarget{StageID: 4, TimeRange: &model.TimeRange{StartTime: &start, EndTime: &end}})
	calls = calculate("a target added and one removed", globalMatrixPathIncremental, globalMatrixReasonNone)
	expectCalls("a target added and one removed", calls,
		fmt.Sprintf("partials split %d from %d targets %d", d, d, all-1), fmt.Sprintf("partials split %d stage-first targets 1 stages [4]", d),
		fmt.Sprintf("partials split %d from %d targets %d", d, d, all-1), fmt.Sprintf("partials split %d stage-first targets 1 stages [4]", d))
	for _, sourceCategory := range sourceCategories {
		b, _ := base(sourceCategory)
		_, inTargets := b.Targets[5]
		_, inTimes := b.Partials.TimesByStageID[5]
		_, inQuantities := b.Partials.QuantitiesByStageID[5]
		if inTargets || inTimes || inQuantities {
			t.Fatalf("%s: the base keeps a removed target stage (targets %v, times %v, quantities %v)", sourceCategory, inTargets, inTimes, inQuantities)
		}
	}

	// more stages changed than GlobalMatrixMaxChangedStages: rebuilt
	setRange(1, changedStart)
	setRange(4, changedStart)
	calculate("stages changed", globalMatrixPathRebuild, globalMatrixReasonChangedStages)

	// the clock went back a day: rebuilt
	now = now.Add(-24 * time.Hour)
	calculate("clock back", globalMatrixPathRebuild, globalMatrixReasonDayBack)
	expectBase("clock back", d-1)
	now = now.Add(24 * time.Hour)
	calculate("clock forward", globalMatrixPathIncremental, globalMatrixReasonNone)

	// older than the max age: rebuilt
	conf.GlobalMatrixBaseMaxAge = time.Hour
	now = now.Add(2 * time.Hour)
	calculate("max age", globalMatrixPathRebuild, globalMatrixReasonMaxAge)
	calculate("after max age", globalMatrixPathIncremental, globalMatrixReasonNone)
	conf.GlobalMatrixBaseMaxAge = 1000 * time.Hour

	// the triggers missing: every day, without bases; those of every source category of the server are dropped,
	// not those of another server nor those of the drop matrices
	cache.GlobalPatternMatrixBase.Set(globalPatternMatrixBaseKey(server, constant.SourceCategoryAutomated), model.GlobalPatternMatrixBase{}, time.Hour)
	cache.GlobalPatternMatrixBase.Set(globalPatternMatrixBaseKey("US", constant.SourceCategoryAll), model.GlobalPatternMatrixBase{}, time.Hour)
	cache.GlobalDropMatrixBase.Set(globalDropMatrixBaseKey(server, constant.SourceCategoryAll), model.GlobalDropMatrixBase{}, time.Hour)
	t.Cleanup(func() { _ = cache.GlobalDropMatrixBase.Flush() })
	store.missing = "disabled"
	calls = calculate("triggers missing", globalMatrixPathFull, globalMatrixReasonTriggersMissing)
	expectCalls("triggers missing", calls, twice(fmt.Sprintf("reference targets %d", all))...)
	for _, sourceCategory := range append(sourceCategories, constant.SourceCategoryAutomated) {
		if _, ok := base(sourceCategory); ok {
			t.Fatalf("%s: the triggers are missing, and the base is kept", sourceCategory)
		}
	}
	if !cache.GlobalPatternMatrixBase.Has(globalPatternMatrixBaseKey("US", constant.SourceCategoryAll)) {
		t.Fatal("the triggers of CN are missing, and the base of US is dropped")
	}
	if !cache.GlobalDropMatrixBase.Has(globalDropMatrixBaseKey(server, constant.SourceCategoryAll)) {
		t.Fatal("the triggers of the pattern matrix elements are missing, and the base of the drop matrix is dropped")
	}
	calculate("triggers still missing", globalMatrixPathFull, globalMatrixReasonTriggersMissing)
	store.missing, store.identity = "", "triggers-3"
	calculate("triggers back", globalMatrixPathRebuild, globalMatrixReasonNoBase)

	// a base that is wrong (it cannot be, but for a bug) with the fingerprint of the days: verified every time, the
	// mismatch is counted, the reference served and the base dropped
	corrupt := func() {
		b, _ := base(constant.SourceCategoryAll)
		corrupted := b
		corrupted.Partials = *addGlobalPatternMatrixAggregates(&b.Partials, &model.GlobalPatternMatrixAggregates{
			QuantitiesByStageID: map[int]map[int]int{1: {1: 1}},
		})
		cache.GlobalPatternMatrixBase.Set(globalPatternMatrixBaseKey(server, constant.SourceCategoryAll), corrupted, time.Hour)
	}
	conf.GlobalMatrixVerifyEvery = 1
	corrupt()
	mismatches, manualMismatches := mismatchCount(constant.SourceCategoryAll), mismatchCount(constant.SourceCategoryManual)
	calls = calculate("verified, corrupted", globalMatrixPathIncremental, globalMatrixReasonNone)
	if mismatchCount(constant.SourceCategoryAll) != mismatches+1 || mismatchCount(constant.SourceCategoryManual) != manualMismatches {
		t.Fatal("the mismatch of a corrupted base was not counted, or counted for another source category")
	}
	expectCalls("verified, corrupted", calls,
		fmt.Sprintf("partials split %d from %d targets %d", d, d, all), fmt.Sprintf("reference targets %d", all),
		fmt.Sprintf("partials split %d from %d targets %d", d, d, all), fmt.Sprintf("reference targets %d", all))
	if _, ok := base(constant.SourceCategoryAll); ok {
		t.Fatal("a base that differs from the reference is kept")
	}
	if _, ok := base(constant.SourceCategoryManual); !ok {
		t.Fatal("a base that is the reference is dropped")
	}

	// every second calculation is verified
	conf.GlobalMatrixVerifyEvery = 2
	s.incremental.calculations.Delete(server)
	if err := cache.GlobalPatternMatrixBase.Flush(); err != nil {
		t.Fatal(err)
	}
	calls = calculate("rebuilt after the mismatch", globalMatrixPathRebuild, globalMatrixReasonNoBase)
	calls = append(calls, calculate("verified every second time", globalMatrixPathIncremental, globalMatrixReasonNone)...)
	references := 0
	for _, call := range calls {
		if call.kind == "reference" {
			references++
		}
	}
	if references != len(sourceCategories) {
		t.Fatalf("two calculations, one verified, ran %d reference aggregations, want %d", references, len(sourceCategories))
	}

	// verify serves the reference, also of a corrupted base
	conf.GlobalMatrixIncremental, conf.GlobalMatrixVerifyEvery = appconfig.GlobalMatrixIncrementalVerify, 0
	corrupt()
	calculate("verify, corrupted", globalMatrixPathIncremental, globalMatrixReasonNone)
	if mismatchCount(constant.SourceCategoryAll) != mismatches+2 {
		t.Fatal("the mismatch of a corrupted base was not counted in verify mode")
	}
	if err := cache.GlobalPatternMatrixBase.Flush(); err != nil {
		t.Fatal(err)
	}
	calls = calculate("verify, rebuilt", globalMatrixPathRebuild, globalMatrixReasonNoBase)
	expectCalls("verify, rebuilt", calls,
		fmt.Sprintf("partials split %d targets %d", d, all), fmt.Sprintf("reference targets %d", all),
		fmt.Sprintf("partials split %d targets %d", d, all), fmt.Sprintf("reference targets %d", all))
	if mismatchCount(constant.SourceCategoryAll) != mismatches+2 || mismatchCount(constant.SourceCategoryManual) != manualMismatches {
		t.Fatal("a mismatch counted without a corrupted base")
	}
}

// addGlobalPatternMatrixAggregates is Add, in new maps: neither side is modified, and a stage of a single side keeps
// its quantities.
func TestAddGlobalPatternMatrixAggregates(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	for i := 0; i < 200; i++ {
		a, b := randomPatternAggregates(r), randomPatternAggregates(r)
		before := describeAll(a) + " + " + describeAll(b)
		got := addGlobalPatternMatrixAggregates(a, b)
		if describeAll(a)+" + "+describeAll(b) != before {
			t.Fatalf("adding modified a side: %s, now %s + %s", before, describeAll(a), describeAll(b))
		}
		want := &model.GlobalPatternMatrixAggregates{}
		want.Add(a)
		want.Add(b)
		if describeAll(got) != describeAll(want) {
			t.Fatalf("%s: got %s, want %s", before, describeAll(got), describeAll(want))
		}
		// a stage's quantities in the sum are of no side, if it is in both
		for stageId, quantities := range got.QuantitiesByStageID {
			_, inA := a.QuantitiesByStageID[stageId]
			_, inB := b.QuantitiesByStageID[stageId]
			if inA && inB {
				quantities[-1] = 1
				if _, ok := a.QuantitiesByStageID[stageId][-1]; ok {
					t.Fatal("the sum shares the quantities of a stage of both sides with a")
				}
				if _, ok := b.QuantitiesByStageID[stageId][-1]; ok {
					t.Fatal("the sum shares the quantities of a stage of both sides with b")
				}
			}
		}
	}
	if got := addGlobalPatternMatrixAggregates(nil, nil); got.TimesByStageID == nil || got.QuantitiesByStageID == nil {
		t.Fatal("the sum of none has nil maps")
	}
}

func randomPatternAggregates(r *rand.Rand) *model.GlobalPatternMatrixAggregates {
	a := &model.GlobalPatternMatrixAggregates{}
	if r.Intn(4) == 0 {
		return a
	}
	a.TimesByStageID, a.QuantitiesByStageID = make(map[int]int), make(map[int]map[int]int)
	for stageId := 1; stageId <= 3; stageId++ {
		if r.Intn(2) == 0 {
			a.TimesByStageID[stageId] = r.Intn(10)
		}
		if r.Intn(2) == 0 {
			a.QuantitiesByStageID[stageId] = map[int]int{1 + r.Intn(3): 1 + r.Intn(5)}
		}
	}
	return a
}

func describeAll(a *model.GlobalPatternMatrixAggregates) string {
	return describeGlobalPatternMatrixAggregates(fakePatternTargets(time.Unix(0, 0), time.Unix(1, 0)), a)
}

func removePatternTarget(targets []*model.GlobalPatternMatrixTarget, stageId int) []*model.GlobalPatternMatrixTarget {
	kept := make([]*model.GlobalPatternMatrixTarget, 0, len(targets))
	for _, target := range targets {
		if target.StageID != stageId {
			kept = append(kept, target)
		}
	}
	return kept
}

// newPatternRebuildTestStore is newRebuildTestStore for the pattern matrices.
func newPatternRebuildTestStore(t *testing.T) (*fakeGlobalPatternMatrixStore, *PatternMatrix, []*model.GlobalPatternMatrixTarget, func(seed int64)) {
	t.Helper()
	cache.Initialize()
	if err := cache.GlobalPatternMatrixBase.Flush(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		util.Now = time.Now
		_ = cache.GlobalPatternMatrixBase.Flush()
	})
	now := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC)
	util.Now = func() time.Time { return now }
	start, end := now.Add(-30*24*time.Hour), now.Add(30*24*time.Hour)
	store := &fakeGlobalPatternMatrixStore{versions: make(map[int]int64), identity: "triggers", gate: make(chan struct{}), started: make(chan struct{})}
	r := rand.New(rand.NewSource(3))
	today := util.GetDayNum(&now, "CN")
	for day := today - 4; day <= today; day++ {
		store.replaceDay(day, fakePatternDay(r, day, start, end))
	}
	s := &PatternMatrix{
		Config:                   &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{GlobalMatrixIncremental: appconfig.GlobalMatrixIncrementalOn}},
		GlobalMatrixFingerprints: store, globalPatternMatrixStore: store,
	}
	batch := func(seed int64) {
		store.replaceDay(today, fakePatternDay(rand.New(rand.NewSource(seed)), today, start, end))
	}
	return store, s, fakePatternTargets(start, end), batch
}

type incrementalPatternCalculated struct {
	aggregates map[string]*model.GlobalPatternMatrixAggregates
	err        error
}

func calculatePatternsIncrementally(ctx context.Context, s *PatternMatrix, targets []*model.GlobalPatternMatrixTarget, refresh bool) <-chan incrementalPatternCalculated {
	done := make(chan incrementalPatternCalculated, 1)
	go func() {
		aggregates, err := s.aggregateGlobalPatternMatricesIncrementally(ctx, "CN", targets, []string{constant.SourceCategoryAll}, refresh)
		done <- incrementalPatternCalculated{aggregates, err}
	}()
	return done
}

func requireGlobalPatternMatrixAggregates(t *testing.T, what string, targets []*model.GlobalPatternMatrixTarget, got, want map[string]*model.GlobalPatternMatrixAggregates) {
	t.Helper()
	if mismatches := diffGlobalPatternMatrixAggregates(targets, got[constant.SourceCategoryAll], want[constant.SourceCategoryAll]); len(mismatches) > 0 {
		t.Fatalf("%s differs from the reference: %v", what, mismatches)
	}
}

// Two requests (with all and with the top patterns) and a refresh needing the rebuild of a base at once: it runs
// once, for the refresh and the request that started after it.
func TestGlobalPatternMatrixIncrementalSingleRebuild(t *testing.T) {
	store, s, targets, _ := newPatternRebuildTestStore(t)
	refreshed := calculatePatternsIncrementally(context.Background(), s, targets, true)
	<-store.started
	requested := calculatePatternsIncrementally(context.Background(), s, targets, false)
	requestedToo := calculatePatternsIncrementally(context.Background(), s, targets, false)
	time.Sleep(200 * time.Millisecond) // the requests wait for the rebuild of the refresh
	close(store.gate)
	_, want := store.aggregate(context.Background(), targets, []string{constant.SourceCategoryAll}, nil)
	for what, done := range map[string]<-chan incrementalPatternCalculated{"the refresh": refreshed, "a request": requested, "the other request": requestedToo} {
		got := <-done
		if got.err != nil {
			t.Fatal(got.err)
		}
		requireGlobalPatternMatrixAggregates(t, what, targets, got.aggregates, want)
	}
	if calls := store.takeCalls(); len(calls) != 1 {
		t.Fatalf("three calculations ran %d aggregations, want a single rebuild: %+v", len(calls), calls)
	}
}

// A refresh after a batch while a request rebuilds from a snapshot of before it rebuilds from a snapshot of its own.
func TestGlobalPatternMatrixIncrementalRefreshAfterRequestRebuild(t *testing.T) {
	store, s, targets, batch := newPatternRebuildTestStore(t)
	store.once = true
	sourceCategories := []string{constant.SourceCategoryAll}
	_, before := store.aggregate(context.Background(), targets, sourceCategories, nil)
	requested := calculatePatternsIncrementally(context.Background(), s, targets, false)
	<-store.started // the request read its snapshot

	batch(4)
	_, after := store.aggregate(context.Background(), targets, sourceCategories, nil)
	if len(diffGlobalPatternMatrixAggregates(targets, before[constant.SourceCategoryAll], after[constant.SourceCategoryAll])) == 0 {
		t.Fatal("the batch did not change the aggregates: the test does not exercise the snapshot")
	}
	refreshed := calculatePatternsIncrementally(context.Background(), s, targets, true)
	var got incrementalPatternCalculated
	select {
	case got = <-refreshed:
	case <-time.After(5 * time.Second):
		t.Error("the refresh waits for the rebuild of the request")
		close(store.gate)
		got = <-refreshed
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	requireGlobalPatternMatrixAggregates(t, "the refresh", targets, got.aggregates, after)

	select {
	case <-store.gate:
	default:
		close(store.gate)
	}
	got = <-requested
	if got.err != nil {
		t.Fatal(got.err)
	}
	requireGlobalPatternMatrixAggregates(t, "the request (its snapshot)", targets, got.aggregates, before)
	if calls := store.takeCalls(); len(calls) != 2 {
		t.Fatalf("the request and the refresh ran %d aggregations, want a rebuild each: %+v", len(calls), calls)
	}
}
