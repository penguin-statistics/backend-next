package service

import (
	"context"
	"errors"
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
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/util"
)

// fakeDropElement is a drop matrix element of the fake store; its time range is in seconds.
type fakeDropElement struct {
	sourceCategory  string
	stageId, itemId int
	dayNum          int
	start, end      int64
	times, quantity int
	quantityBuckets map[int]int
}

type fakeDropMatrixCall struct {
	kind    string // reference or partials
	scope   model.GlobalMatrixScope
	targets int
	stages  []int
}

// fakeGlobalDropMatrixStore aggregates elements in memory as the statements of the repo do, versions their days as
// the triggers do, and records the aggregations it runs. RunInSnapshotTx reads the elements and versions as they are
// when it starts, as a snapshot.
type fakeGlobalDropMatrixStore struct {
	mu          sync.Mutex
	elements    []fakeDropElement
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

// fakeSnapshot is what a snapshot of the fake store reads.
type fakeSnapshot struct {
	elements []fakeDropElement
	versions map[int]int64
}

type fakeSnapshotKey struct{}

// read returns the elements and versions of the snapshot of ctx, or, without one, the current ones. f.mu is held.
func (f *fakeGlobalDropMatrixStore) read(ctx context.Context) ([]fakeDropElement, map[int]int64) {
	if snapshot, ok := ctx.Value(fakeSnapshotKey{}).(*fakeSnapshot); ok {
		return snapshot.elements, snapshot.versions
	}
	return f.elements, f.versions
}

func (f *fakeGlobalDropMatrixStore) bump(dayNum int) {
	f.nextVersion++
	f.versions[dayNum] = f.nextVersion
}

// replaceDay replaces the elements of a day of a source category, as a writer does.
func (f *fakeGlobalDropMatrixStore) replaceDay(dayNum int, elements []fakeDropElement) {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.elements[:0:0]
	for _, el := range f.elements {
		if el.dayNum != dayNum {
			kept = append(kept, el)
		}
	}
	f.elements = append(kept, elements...)
	f.bump(dayNum)
}

func (f *fakeGlobalDropMatrixStore) RunInSnapshotTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error {
	f.mu.Lock()
	// replaceDay replaces f.elements, never modifies it
	snapshot := &fakeSnapshot{elements: f.elements, versions: make(map[int]int64, len(f.versions))}
	for dayNum, version := range f.versions {
		snapshot.versions[dayNum] = version
	}
	f.mu.Unlock()
	return fn(context.WithValue(ctx, fakeSnapshotKey{}, snapshot), bun.Tx{})
}

func (f *fakeGlobalDropMatrixStore) aggregate(
	ctx context.Context, targets []*model.GlobalDropMatrixTarget, sourceCategories []string, scope *model.GlobalMatrixScope,
) (before, after map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	elements, _ := f.read(ctx)
	before = make(map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate)
	after = make(map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate)
	type distinctTimes struct {
		key          model.GlobalDropMatrixKey
		before       bool
		times, dayNo int
	}
	seen := make(map[distinctTimes]bool)
	for _, target := range targets {
		for _, el := range elements {
			if el.stageId != target.StageID || el.itemId != target.ItemID || !containsString(sourceCategories, el.sourceCategory) ||
				el.start < target.TimeRange.StartTime.Unix() || el.end > target.TimeRange.EndTime.Unix() {
				continue
			}
			if scope != nil && scope.FromDayNum.Valid && int64(el.dayNum) < scope.FromDayNum.Int64 {
				continue
			}
			side := after
			isBefore := scope != nil && el.dayNum < scope.SplitDayNum
			if isBefore {
				side = before
			}
			if el.times <= 0 && el.quantity <= 0 {
				continue
			}
			key := model.GlobalDropMatrixKey{SourceCategory: el.sourceCategory, StageID: el.stageId, ItemID: el.itemId}
			aggregate, ok := side[key]
			if !ok {
				aggregate = &model.GlobalDropMatrixAggregate{QuantityBuckets: make(map[int]int)}
				side[key] = aggregate
			}
			if d := (distinctTimes{key, isBefore, el.times, el.dayNum}); el.times > 0 && !seen[d] {
				seen[d] = true
				aggregate.Times = null.IntFrom(aggregate.Times.Int64 + int64(el.times))
			}
			if el.quantity > 0 {
				aggregate.Quantity = null.IntFrom(aggregate.Quantity.Int64 + int64(el.quantity))
				for quantity, count := range el.quantityBuckets {
					aggregate.QuantityBuckets[quantity] += count
				}
			}
		}
	}
	return before, after
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func (f *fakeGlobalDropMatrixStore) record(call fakeDropMatrixCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeGlobalDropMatrixStore) takeCalls() []fakeDropMatrixCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := f.calls
	f.calls = nil
	return calls
}

func (f *fakeGlobalDropMatrixStore) CalcGlobalDropMatrixAggregates(
	ctx context.Context, _ bun.IDB, _ string, targets []*model.GlobalDropMatrixTarget, sourceCategories []string,
) (map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate, error) {
	f.record(fakeDropMatrixCall{kind: "reference", targets: len(targets)})
	_, all := f.aggregate(ctx, targets, sourceCategories, nil)
	return all, nil
}

func (f *fakeGlobalDropMatrixStore) CalcGlobalDropMatrixPartials(
	ctx context.Context, _ bun.IDB, _ string, targets []*model.GlobalDropMatrixTarget, sourceCategories []string, scope model.GlobalMatrixScope,
) (before, after map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate, err error) {
	if len(targets) == 0 {
		return map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate{}, map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate{}, nil
	}
	stages := make(map[int]bool)
	for _, target := range targets {
		stages[target.StageID] = true
	}
	call := fakeDropMatrixCall{kind: "partials", scope: scope, targets: len(targets)}
	for stageId := range stages {
		call.stages = append(call.stages, stageId)
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

func (f *fakeGlobalDropMatrixStore) Triggers(context.Context, bun.IDB, string) (identity, missing string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.identity, f.missing, nil
}

func (f *fakeGlobalDropMatrixStore) DayFingerprints(ctx context.Context, _ bun.IDB, _, _ string, bounds []int) (map[int]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, versions := f.read(ctx)
	return fakeDayFingerprints(versions, bounds), nil
}

// fakeDayFingerprints returns for each of bounds the versions of the days before it.
func fakeDayFingerprints(versions map[int]int64, bounds []int) map[int]string {
	fingerprints := make(map[int]string, len(bounds))
	for _, bound := range bounds {
		days := make([]int, 0)
		for dayNum := range versions {
			if dayNum < bound {
				days = append(days, dayNum)
			}
		}
		sort.Ints(days)
		var b strings.Builder
		for _, dayNum := range days {
			fmt.Fprintf(&b, "%d:%d,", dayNum, versions[dayNum])
		}
		fingerprints[bound] = b.String()
	}
	return fingerprints
}

// fakeTargets makes a target per stage 1..4 and item 10..12, plus one without elements, over [start, end).
func fakeTargets(start, end time.Time) []*model.GlobalDropMatrixTarget {
	targets := make([]*model.GlobalDropMatrixTarget, 0)
	for stageId := 1; stageId <= 4; stageId++ {
		for itemId := 10; itemId <= 12; itemId++ {
			targets = append(targets, &model.GlobalDropMatrixTarget{StageID: stageId, ItemID: itemId, TimeRange: &model.TimeRange{StartTime: &start, EndTime: &end}})
		}
	}
	return append(targets, &model.GlobalDropMatrixTarget{StageID: 9, ItemID: 99, TimeRange: &model.TimeRange{StartTime: &start, EndTime: &end}})
}

// fakeDay makes random elements of a day, of the targets' stages and items, some outside their time range, some
// with zero times or quantity.
func fakeDay(r *rand.Rand, dayNum int, start, end time.Time) []fakeDropElement {
	elements := make([]fakeDropElement, 0)
	for _, sourceCategory := range []string{constant.SourceCategoryAll, constant.SourceCategoryManual} {
		for stageId := 1; stageId <= 4; stageId++ {
			times := r.Intn(5)
			for itemId := 10; itemId <= 12; itemId++ {
				if r.Intn(3) == 0 {
					continue
				}
				el := fakeDropElement{
					sourceCategory: sourceCategory, stageId: stageId, itemId: itemId, dayNum: dayNum,
					start: start.Unix() + int64(r.Intn(3600)), end: end.Unix() - int64(r.Intn(3600)), times: times,
				}
				if r.Intn(10) == 0 { // outside of the time range
					el.end = end.Unix() + 1
				}
				if q := r.Intn(4); q > 0 {
					el.quantity = q * times
					el.quantityBuckets = map[int]int{q: times}
				} else {
					el.quantityBuckets = map[int]int{0: times}
				}
				elements = append(elements, el)
			}
		}
	}
	return elements
}

// TestGlobalDropMatrixIncrementalDecisions runs the incremental global drop matrices on a fake store through every
// reason to extend, rebuild or aggregate every day, checking each time the path taken, the aggregations run, the
// bases cached, and that the results are those of the reference aggregation.
func TestGlobalDropMatrixIncrementalDecisions(t *testing.T) {
	cache.Initialize()
	if err := cache.GlobalDropMatrixBase.Flush(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		util.Now = time.Now
		_ = cache.GlobalDropMatrixBase.Flush()
	})
	const server = "CN"
	ctx := context.Background()
	r := rand.New(rand.NewSource(7))
	now := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC) // 10:00 in CN
	util.Now = func() time.Time { return now }
	dayNum := func() int { return util.GetDayNum(&now, server) }

	start, end := now.Add(-30*24*time.Hour), now.Add(30*24*time.Hour)
	targets := fakeTargets(start, end)
	store := &fakeGlobalDropMatrixStore{versions: make(map[int]int64), identity: "triggers-1"}
	for day := dayNum() - 9; day <= dayNum(); day++ {
		store.replaceDay(day, fakeDay(r, day, start, end))
	}
	conf := &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{
		GlobalMatrixIncremental: appconfig.GlobalMatrixIncrementalOn, GlobalMatrixBaseMaxAge: 1000 * time.Hour, GlobalMatrixMaxChangedStages: 1,
	}}
	s := &DropMatrix{Config: conf, GlobalMatrixFingerprints: store, globalDropMatrixStore: store}
	sourceCategories := []string{constant.SourceCategoryAll, constant.SourceCategoryManual}

	pathCount := func(sourceCategory, path, reason string) float64 {
		return testutil.ToFloat64(observability.GlobalMatrixRefreshPaths.WithLabelValues(globalMatrixKindDrop, server, sourceCategory, path, reason))
	}
	mismatchCount := func(sourceCategory string) float64 {
		return testutil.ToFloat64(observability.GlobalMatrixVerifyMismatches.WithLabelValues(globalMatrixKindDrop, server, sourceCategory))
	}
	base := func(sourceCategory string) (model.GlobalDropMatrixBase, bool) {
		var b model.GlobalDropMatrixBase
		err := cache.GlobalDropMatrixBase.Get(globalDropMatrixBaseKey(server, sourceCategory), &b)
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
		got, err := s.aggregateGlobalDropMatricesIncrementally(ctx, server, targets, sourceCategories, false)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		calls := store.takeCalls()
		_, want := store.aggregate(context.Background(), targets, sourceCategories, nil)
		for _, sourceCategory := range sourceCategories {
			if mismatches := diffGlobalDropMatrixAggregates(targets, globalDropMatrixAggregatesOf(got, sourceCategory), globalDropMatrixAggregatesOf(want, sourceCategory)); len(mismatches) > 0 {
				t.Fatalf("%s %s: the aggregates differ from the reference: %v", what, sourceCategory, mismatches)
			}
			gotLines := globalDropMatrixResultLines(assembleGlobalDropMatrices(targets, got, sourceCategories)[sourceCategory])
			wantLines := globalDropMatrixResultLines(assembleGlobalDropMatrices(targets, want, sourceCategories)[sourceCategory])
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
	all := len(targets)

	// no base: a rebuild per source category, over every day, the days before yesterday apart
	calls := calculate("no base", globalMatrixPathRebuild, globalMatrixReasonNoBase)
	d := dayNum() - 1
	expectCalls("no base", calls, fmt.Sprintf("partials split %d targets %d", d, all), fmt.Sprintf("partials split %d targets %d", d, all))
	expectBase("no base", d)

	// nothing changed: the days since the base only, the target without elements is not recomputed
	calls = calculate("unchanged", globalMatrixPathIncremental, globalMatrixReasonNone)
	expectCalls("unchanged", calls, fmt.Sprintf("partials split %d from %d targets %d", d, d, all), fmt.Sprintf("partials split %d from %d targets %d", d, d, all))

	// today and yesterday rewritten (the worker): still the days since the base only; the old base is not modified
	oldBase, _ := base(constant.SourceCategoryAll)
	oldDescription := describeBase(oldBase)
	store.replaceDay(dayNum(), fakeDay(r, dayNum(), start, end))
	store.replaceDay(dayNum()-1, fakeDay(r, dayNum()-1, start, end))
	calculate("today and yesterday rewritten", globalMatrixPathIncremental, globalMatrixReasonNone)
	if describeBase(oldBase) != oldDescription {
		t.Fatal("extending a base modified it")
	}

	// the next day: the day settled since is added to the base
	now = now.Add(24 * time.Hour)
	store.replaceDay(dayNum(), fakeDay(r, dayNum(), start, end))
	calls = calculate("next day", globalMatrixPathIncremental, globalMatrixReasonNone)
	expectCalls("next day", calls, fmt.Sprintf("partials split %d from %d targets %d", d+1, d, all), fmt.Sprintf("partials split %d from %d targets %d", d+1, d, all))
	d++
	expectBase("next day", d)
	if describeBase(oldBase) != oldDescription {
		t.Fatal("extending a base modified it")
	}

	// a day of the base rewritten (an admin recalculation, a repair): rebuilt
	store.replaceDay(dayNum()-5, fakeDay(r, dayNum()-5, start, end))
	calculate("old day rewritten", globalMatrixPathRebuild, globalMatrixReasonFingerprint)
	expectBase("old day rewritten", d)

	// the triggers disabled and enabled again: another identity, the same versions; rebuilt
	store.identity = "triggers-2"
	calculate("trigger identity changed", globalMatrixPathRebuild, globalMatrixReasonFingerprint)

	// the time range of a stage's targets changed: that stage is aggregated over every day, the others since the base
	changedStart := start.Add(time.Hour)
	for _, target := range targets {
		if target.StageID == 2 {
			target.TimeRange = &model.TimeRange{StartTime: &changedStart, EndTime: &end}
		}
	}
	calls = calculate("a stage changed", globalMatrixPathIncremental, globalMatrixReasonNone)
	expectCalls("a stage changed", calls,
		fmt.Sprintf("partials split %d from %d targets %d", d, d, all-3), fmt.Sprintf("partials split %d stage-first targets 3 stages [2]", d),
		fmt.Sprintf("partials split %d from %d targets %d", d, d, all-3), fmt.Sprintf("partials split %d stage-first targets 3 stages [2]", d))

	// a target added and one removed, of the same stage
	targets = append(targets, &model.GlobalDropMatrixTarget{StageID: 3, ItemID: 13, TimeRange: &model.TimeRange{StartTime: &start, EndTime: &end}})
	targets = removeTarget(targets, 3, 10)
	store.replaceDay(dayNum()-3, append(fakeDay(r, dayNum()-3, start, end), fakeDropElement{
		sourceCategory: constant.SourceCategoryAll, stageId: 3, itemId: 13, dayNum: dayNum() - 3, start: start.Unix(), end: end.Unix(), times: 5, quantity: 2, quantityBuckets: map[int]int{1: 2},
	}, fakeDropElement{
		sourceCategory: constant.SourceCategoryManual, stageId: 3, itemId: 13, dayNum: dayNum() - 3, start: start.Unix(), end: end.Unix(), times: 5, quantity: 2, quantityBuckets: map[int]int{1: 2},
	}))
	calculate("old day rewritten, again", globalMatrixPathRebuild, globalMatrixReasonFingerprint)
	targets = append(targets, &model.GlobalDropMatrixTarget{StageID: 3, ItemID: 10, TimeRange: &model.TimeRange{StartTime: &start, EndTime: &end}})
	targets = removeTarget(targets, 3, 13)
	calls = calculate("a target added and one removed", globalMatrixPathIncremental, globalMatrixReasonNone)
	expectCalls("a target added and one removed", calls,
		fmt.Sprintf("partials split %d from %d targets %d", d, d, all-1), fmt.Sprintf("partials split %d stage-first targets 1 stages [3]", d),
		fmt.Sprintf("partials split %d from %d targets %d", d, d, all-1), fmt.Sprintf("partials split %d stage-first targets 1 stages [3]", d))
	for _, sourceCategory := range sourceCategories {
		b, _ := base(sourceCategory)
		if _, ok := b.Targets[model.GlobalMatrixStageItem{StageID: 3, ItemID: 13}]; ok {
			t.Fatalf("%s: the base keeps a removed target", sourceCategory)
		}
		if _, ok := b.Partials[model.GlobalMatrixStageItem{StageID: 3, ItemID: 13}]; ok {
			t.Fatalf("%s: the base keeps the aggregate of a removed target", sourceCategory)
		}
	}

	// the targets of more stages changed than GlobalMatrixMaxChangedStages: rebuilt
	for _, target := range targets {
		if target.StageID == 1 || target.StageID == 4 {
			target.TimeRange = &model.TimeRange{StartTime: &changedStart, EndTime: &end}
		}
	}
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
	// not those of another server
	cache.GlobalDropMatrixBase.Set(globalDropMatrixBaseKey(server, constant.SourceCategoryAutomated), model.GlobalDropMatrixBase{}, time.Hour)
	cache.GlobalDropMatrixBase.Set(globalDropMatrixBaseKey("US", constant.SourceCategoryAll), model.GlobalDropMatrixBase{}, time.Hour)
	store.missing = "disabled"
	calls = calculate("triggers missing", globalMatrixPathFull, globalMatrixReasonTriggersMissing)
	expectCalls("triggers missing", calls, fmt.Sprintf("reference targets %d", all), fmt.Sprintf("reference targets %d", all))
	for _, sourceCategory := range append(sourceCategories, constant.SourceCategoryAutomated) {
		if _, ok := base(sourceCategory); ok {
			t.Fatalf("%s: the triggers are missing, and the base is kept", sourceCategory)
		}
	}
	if !cache.GlobalDropMatrixBase.Has(globalDropMatrixBaseKey("US", constant.SourceCategoryAll)) {
		t.Fatal("the triggers of CN are missing, and the base of US is dropped")
	}
	calculate("triggers still missing", globalMatrixPathFull, globalMatrixReasonTriggersMissing)
	store.missing, store.identity = "", "triggers-3"
	calculate("triggers back", globalMatrixPathRebuild, globalMatrixReasonNoBase)

	// a base that is wrong (it cannot be, but for a bug) with the fingerprint of the days: verified every time, the
	// mismatch is counted, the reference served and the base dropped
	corrupt := func() {
		b, _ := base(constant.SourceCategoryAll)
		corrupted := b
		corrupted.Partials = make(map[model.GlobalMatrixStageItem]*model.GlobalDropMatrixAggregate, len(b.Partials))
		for key, partial := range b.Partials {
			corrupted.Partials[key] = partial
		}
		for key, partial := range b.Partials {
			if partial.Quantity.Valid {
				wrong := partial.Clone()
				wrong.Quantity.Int64++
				corrupted.Partials[key] = wrong
				break
			}
		}
		cache.GlobalDropMatrixBase.Set(globalDropMatrixBaseKey(server, constant.SourceCategoryAll), corrupted, time.Hour)
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
	if err := cache.GlobalDropMatrixBase.Flush(); err != nil {
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
	if err := cache.GlobalDropMatrixBase.Flush(); err != nil {
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

// newRebuildTestStore returns a store whose rebuilds wait for its gate, with days of elements until today, and a
// DropMatrix calculating incrementally from it, without bases, and a batch, which rewrites today.
func newRebuildTestStore(t *testing.T) (*fakeGlobalDropMatrixStore, *DropMatrix, []*model.GlobalDropMatrixTarget, func(seed int64)) {
	t.Helper()
	cache.Initialize()
	if err := cache.GlobalDropMatrixBase.Flush(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		util.Now = time.Now
		_ = cache.GlobalDropMatrixBase.Flush()
	})
	now := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC)
	util.Now = func() time.Time { return now }
	start, end := now.Add(-30*24*time.Hour), now.Add(30*24*time.Hour)
	store := &fakeGlobalDropMatrixStore{versions: make(map[int]int64), identity: "triggers", gate: make(chan struct{}), started: make(chan struct{})}
	r := rand.New(rand.NewSource(3))
	today := util.GetDayNum(&now, "CN")
	for day := today - 4; day <= today; day++ {
		store.replaceDay(day, fakeDay(r, day, start, end))
	}
	s := &DropMatrix{
		Config:                   &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{GlobalMatrixIncremental: appconfig.GlobalMatrixIncrementalOn}},
		GlobalMatrixFingerprints: store, globalDropMatrixStore: store,
	}
	batch := func(seed int64) {
		store.replaceDay(today, fakeDay(rand.New(rand.NewSource(seed)), today, start, end))
	}
	return store, s, fakeTargets(start, end), batch
}

type incrementalCalculated struct {
	aggregates map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate
	err        error
}

func calculateIncrementally(ctx context.Context, s *DropMatrix, targets []*model.GlobalDropMatrixTarget, refresh bool) <-chan incrementalCalculated {
	done := make(chan incrementalCalculated, 1)
	go func() {
		aggregates, err := s.aggregateGlobalDropMatricesIncrementally(ctx, "CN", targets, []string{constant.SourceCategoryAll}, refresh)
		done <- incrementalCalculated{aggregates, err}
	}()
	return done
}

func requireGlobalDropMatrixAggregates(t *testing.T, what string, targets []*model.GlobalDropMatrixTarget, got, want map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate) {
	t.Helper()
	if mismatches := diffGlobalDropMatrixAggregates(targets, globalDropMatrixAggregatesOf(got, constant.SourceCategoryAll), globalDropMatrixAggregatesOf(want, constant.SourceCategoryAll)); len(mismatches) > 0 {
		t.Fatalf("%s differs from the reference: %v", what, mismatches)
	}
}

// A request needing the rebuild a refresh is running waits for it and takes its results: the rebuild runs once.
func TestGlobalDropMatrixIncrementalSingleRebuild(t *testing.T) {
	store, s, targets, _ := newRebuildTestStore(t)
	refreshed := calculateIncrementally(context.Background(), s, targets, true)
	<-store.started
	requested := calculateIncrementally(context.Background(), s, targets, false)
	time.Sleep(200 * time.Millisecond) // the request waits for the rebuild of the refresh
	close(store.gate)
	_, want := store.aggregate(context.Background(), targets, []string{constant.SourceCategoryAll}, nil)
	for what, done := range map[string]<-chan incrementalCalculated{"the refresh": refreshed, "the request": requested} {
		got := <-done
		if got.err != nil {
			t.Fatal(got.err)
		}
		requireGlobalDropMatrixAggregates(t, what, targets, got.aggregates, want)
	}
	if calls := store.takeCalls(); len(calls) != 1 {
		t.Fatalf("two calculations ran %d aggregations, want a single rebuild: %+v", len(calls), calls)
	}
}

// A refresh after a batch while a request rebuilds from a snapshot of before it does not take the request's results:
// they lack the batch the refresh publishes. It rebuilds from a snapshot of its own.
func TestGlobalDropMatrixIncrementalRefreshAfterRequestRebuild(t *testing.T) {
	store, s, targets, batch := newRebuildTestStore(t)
	store.once = true
	sourceCategories := []string{constant.SourceCategoryAll}
	_, before := store.aggregate(context.Background(), targets, sourceCategories, nil)
	requested := calculateIncrementally(context.Background(), s, targets, false)
	<-store.started // the request read its snapshot

	batch(4)
	_, after := store.aggregate(context.Background(), targets, sourceCategories, nil)
	if len(diffGlobalDropMatrixAggregates(targets, globalDropMatrixAggregatesOf(before, constant.SourceCategoryAll), globalDropMatrixAggregatesOf(after, constant.SourceCategoryAll))) == 0 {
		t.Fatal("the batch did not change the aggregates: the test does not exercise the snapshot")
	}
	refreshed := calculateIncrementally(context.Background(), s, targets, true)
	var got incrementalCalculated
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
	requireGlobalDropMatrixAggregates(t, "the refresh", targets, got.aggregates, after)

	select {
	case <-store.gate:
	default:
		close(store.gate)
	}
	got = <-requested
	if got.err != nil {
		t.Fatal(got.err)
	}
	requireGlobalDropMatrixAggregates(t, "the request (its snapshot)", targets, got.aggregates, before)
	if calls := store.takeCalls(); len(calls) != 2 {
		t.Fatalf("the request and the refresh ran %d aggregations, want a rebuild each: %+v", len(calls), calls)
	}
}

// A request waiting for a rebuild that failed because the context of its caller was canceled (a refresh past its
// deadline) rebuilds instead of failing too.
func TestGlobalDropMatrixIncrementalRebuildOfCanceledCaller(t *testing.T) {
	store, s, targets, _ := newRebuildTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	refreshed := calculateIncrementally(ctx, s, targets, true)
	<-store.started
	requested := calculateIncrementally(context.Background(), s, targets, false)
	time.Sleep(200 * time.Millisecond) // the request waits for the rebuild of the refresh
	cancel()
	close(store.gate)
	if got := <-refreshed; !errors.Is(got.err, context.Canceled) {
		t.Fatalf("the canceled refresh returned %v, want %v", got.err, context.Canceled)
	}
	got := <-requested
	if got.err != nil {
		t.Fatalf("the request failed with the refresh: %v", got.err)
	}
	_, want := store.aggregate(context.Background(), targets, []string{constant.SourceCategoryAll}, nil)
	requireGlobalDropMatrixAggregates(t, "the request", targets, got.aggregates, want)
}

func describeFakeCall(call fakeDropMatrixCall) string {
	if call.kind == "reference" {
		return fmt.Sprintf("reference targets %d", call.targets)
	}
	s := fmt.Sprintf("partials split %d", call.scope.SplitDayNum)
	if call.scope.FromDayNum.Valid {
		s += fmt.Sprintf(" from %d", call.scope.FromDayNum.Int64)
	}
	if call.scope.StageFirst {
		return s + fmt.Sprintf(" stage-first targets %d stages %v", call.targets, call.stages)
	}
	return s + fmt.Sprintf(" targets %d", call.targets)
}

func describeBase(b model.GlobalDropMatrixBase) string {
	lines := make([]string, 0, len(b.Partials))
	for key, partial := range b.Partials {
		lines = append(lines, fmt.Sprintf("%d|%d %s", key.StageID, key.ItemID, describeGlobalDropMatrixAggregate(partial)))
	}
	sort.Strings(lines)
	return fmt.Sprintf("%d %s %d %v", b.BeforeDayNum, b.Fingerprint, len(b.Targets), lines)
}

func removeTarget(targets []*model.GlobalDropMatrixTarget, stageId, itemId int) []*model.GlobalDropMatrixTarget {
	kept := make([]*model.GlobalDropMatrixTarget, 0, len(targets))
	for _, target := range targets {
		if target.StageID != stageId || target.ItemID != itemId {
			kept = append(kept, target)
		}
	}
	return kept
}
