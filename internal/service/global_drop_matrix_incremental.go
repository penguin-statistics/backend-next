package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"github.com/uptrace/bun"
	"golang.org/x/sync/singleflight"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/util"
)

// The incremental global drop matrices (GlobalMatrixIncremental, #860). The aggregate of a target over every day is
// the aggregate of the days before any day Add that of the others (model.GlobalDropMatrixAggregate.Add), so the days
// before yesterday are kept aggregated, per process, server and source category, in a base, and a calculation
// aggregates only the days since the base, and every day of the targets whose time range changed. A base is valid
// only while no element of its days changed: every write of the elements gives the days it touched a new version
// (scripts/migrations/20261025_matrix_element_day_versions.sql), in its own transaction, so a calculation reads the
// fingerprint of the versions of the base's days and the elements since in one snapshot, and extends the base only
// if the fingerprint is the base's.

// The paths and reasons of observability.GlobalMatrixRefreshPaths.
const (
	globalMatrixPathIncremental = "incremental"
	globalMatrixPathRebuild     = "rebuild"
	globalMatrixPathFull        = "full"

	globalMatrixReasonNone            = "none"
	globalMatrixReasonNoBase          = "no_base"
	globalMatrixReasonFingerprint     = "fingerprint"
	globalMatrixReasonDayBack         = "day_back"
	globalMatrixReasonMaxAge          = "max_age"
	globalMatrixReasonChangedStages   = "changed_stages"
	globalMatrixReasonTriggersMissing = "triggers_missing"

	globalMatrixKindDrop = "drop"

	// maxLoggedMismatches bounds the keys a verification logs.
	maxLoggedMismatches = 20
)

// GlobalMatrixFingerprints reads, in the snapshot of db, what the bases of the incremental global matrices are
// checked against. The default reads the day versions of the elements; a benchmark on a database without them uses a
// constant one.
type GlobalMatrixFingerprints interface {
	// Triggers returns what identifies the triggers that version the days of the elements of table, or why the day
	// versions cannot be relied on (missing is not empty): a trigger is missing or not firing always, or the versions
	// cannot be read.
	Triggers(ctx context.Context, db bun.IDB, table string) (identity, missing string, err error)
	// DayFingerprints returns, for each of bounds, the fingerprint of the day versions of the elements of server in
	// tbl (drop or pattern) of the days before it.
	DayFingerprints(ctx context.Context, db bun.IDB, tbl, server string, bounds []int) (map[int]string, error)
}

// dayVersionFingerprints reads the day versions the triggers keep.
type dayVersionFingerprints struct{}

func (dayVersionFingerprints) Triggers(ctx context.Context, db bun.IDB, table string) (identity, missing string, err error) {
	enabled, identity, readable, err := repo.ReadMatrixElementDayVersionTriggers(ctx, db, table)
	if err != nil {
		return "", "", err
	}
	if enabled != repo.MatrixElementDayVersionTriggers {
		return identity, fmt.Sprintf("%d of the %d triggers of the day versions of %s fire always", enabled, repo.MatrixElementDayVersionTriggers, table), nil
	}
	if !readable {
		return identity, "the day versions cannot be read (matrix_element_day_versions is missing, or SELECT is not granted)", nil
	}
	return identity, "", nil
}

func (dayVersionFingerprints) DayFingerprints(ctx context.Context, db bun.IDB, tbl, server string, bounds []int) (map[int]string, error) {
	return repo.ReadMatrixElementDayFingerprints(ctx, db, tbl, server, bounds)
}

// globalDropMatrixStore reads the drop matrix elements for the incremental global drop matrices
// (DropMatrixElement).
type globalDropMatrixStore interface {
	// RunInSnapshotTx runs fn in a read-only snapshot transaction, the elements locked from before the snapshot is
	// taken: TRUNCATE is not MVCC-safe, and one committed after the fingerprint was read would empty the elements of
	// the snapshot while the day version it bumps is not in it, so a base would pass the check with no element since.
	RunInSnapshotTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error
	CalcGlobalDropMatrixAggregates(
		ctx context.Context, db bun.IDB, server string, targets []*model.GlobalDropMatrixTarget, sourceCategories []string,
	) (map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate, error)
	CalcGlobalDropMatrixPartials(
		ctx context.Context, db bun.IDB, server string, targets []*model.GlobalDropMatrixTarget, sourceCategories []string,
		scope model.GlobalMatrixScope,
	) (before, after map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate, err error)
}

// globalMatrixIncremental is the state of the incremental global matrices of a process; the zero value is ready.
type globalMatrixIncremental struct {
	// jitter is the fraction of GlobalMatrixBaseMaxAge taken off it in this process, in [0, 0.1)
	jitterOnce sync.Once
	jitter     float64
	// calculations counts the calculations of each server (*atomic.Int64), for GlobalMatrixVerifyEvery
	calculations sync.Map
	// rebuilds runs a rebuild of a server and source category once for the callers that need it at once (see
	// rebuildGlobalDropMatrixBase)
	rebuilds singleflight.Group
}

func (g *globalMatrixIncremental) maxAge(configured time.Duration) time.Duration {
	if configured <= 0 {
		configured = 24 * time.Hour
	}
	g.jitterOnce.Do(func() { g.jitter = rand.Float64() / 10 })
	return configured - time.Duration(float64(configured)*g.jitter)
}

// verifies counts a calculation of server, and reports whether it is verified.
func (g *globalMatrixIncremental) verifies(conf *appconfig.Config, server string) bool {
	if conf.GlobalMatrixIncremental == appconfig.GlobalMatrixIncrementalVerify {
		return true
	}
	if conf.GlobalMatrixVerifyEvery <= 0 {
		return false
	}
	counter, _ := g.calculations.LoadOrStore(server, &atomic.Int64{})
	return counter.(*atomic.Int64).Add(1)%int64(conf.GlobalMatrixVerifyEvery) == 0
}

// globalDropMatrixCalc is the calculation of the global drop matrix of a source category.
type globalDropMatrixCalc struct {
	sourceCategory string
	// base is the cached base, if any
	base *model.GlobalDropMatrixBase
	// path and reason, once decided (path is empty until then; reason may be set before, as the reason to rebuild)
	path, reason string
	// newBase is the base to cache once the calculation succeeded, if any
	newBase *model.GlobalDropMatrixBase
	// aggregates are the aggregates of every day of the targets with elements
	aggregates map[model.GlobalMatrixStageItem]*model.GlobalDropMatrixAggregate
}

func globalDropMatrixBaseKey(server, sourceCategory string) string {
	return server + constant.CacheSep + sourceCategory
}

func (s *DropMatrix) globalMatrixFingerprints() GlobalMatrixFingerprints {
	if s.GlobalMatrixFingerprints != nil {
		return s.GlobalMatrixFingerprints
	}
	return dayVersionFingerprints{}
}

func (s *DropMatrix) globalDropMatrixElements() globalDropMatrixStore {
	if s.globalDropMatrixStore != nil {
		return s.globalDropMatrixStore
	}
	return s.DropMatrixElementService
}

// aggregateGlobalDropMatricesIncrementally aggregates the elements of the targets of server over every day for
// every source category, as aggregateGlobalDropMatrices does, from the cached bases (see the top of the file). For
// each source category:
//   - without a usable base (none, of a later day, older than GlobalMatrixBaseMaxAge), it rebuilds one: one statement
//     over every day, the days before yesterday apart, in a snapshot of its own;
//   - otherwise, in one snapshot for every source category, if the fingerprint of the base's days is still the
//     base's, it extends the base by the days settled since and adds the elements since yesterday, and aggregates
//     every day of the targets whose time range changed (rebuilding if more than GlobalMatrixMaxChangedStages stages
//     changed);
//   - if the triggers of the day versions are missing or not firing always, it aggregates every day as
//     aggregateGlobalDropMatrices does, and drops the bases of server.
//
// A calculation that is verified (GlobalMatrixIncremental verify, every GlobalMatrixVerifyEvery) also runs
// aggregateGlobalDropMatrices's statement in the same snapshot, and if the results differ, serves its results and
// drops the base. verify always serves them.
//
// refresh is for a refresh, which caches its results as the newest: they must be of data at least as new as the call
// (see rebuildGlobalDropMatrixBase).
func (s *DropMatrix) aggregateGlobalDropMatricesIncrementally(
	ctx context.Context, server string, targets []*model.GlobalDropMatrixTarget, sourceCategories []string, refresh bool,
) (map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate, error) {
	now := util.Now()
	// the days before yesterday: the worker rewrites yesterday when the day changes, maybe after this calculation
	beforeDayNum := util.GetDayNum(&now, server) - 1
	verify := s.incremental.verifies(s.Config, server)
	maxAge := s.incremental.maxAge(s.Config.GlobalMatrixBaseMaxAge)
	ranges := globalMatrixTargetRanges(targets)

	calcs := make([]*globalDropMatrixCalc, 0, len(sourceCategories))
	extensible := make([]*globalDropMatrixCalc, 0, len(sourceCategories))
	for _, sourceCategory := range sourceCategories {
		calc := &globalDropMatrixCalc{sourceCategory: sourceCategory}
		calcs = append(calcs, calc)
		var base model.GlobalDropMatrixBase
		if err := cache.GlobalDropMatrixBase.Get(globalDropMatrixBaseKey(server, sourceCategory), &base); err != nil {
			calc.reason = globalMatrixReasonNoBase
			continue
		}
		calc.base = &base
		switch {
		case base.BeforeDayNum > beforeDayNum: // the clock went back
			calc.reason = globalMatrixReasonDayBack
		case now.Sub(base.BuiltAt) > maxAge:
			calc.reason = globalMatrixReasonMaxAge
		default:
			extensible = append(extensible, calc)
		}
	}

	if len(extensible) > 0 {
		if err := s.extendGlobalDropMatrixBases(ctx, server, targets, ranges, beforeDayNum, extensible, verify); err != nil {
			return nil, err
		}
	}
	for _, calc := range calcs {
		if calc.path == "" {
			if err := s.rebuildGlobalDropMatrixBase(ctx, server, targets, ranges, beforeDayNum, calc, verify, refresh); err != nil {
				return nil, err
			}
		}
	}

	aggregates := make(map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate)
	for _, calc := range calcs {
		if calc.newBase != nil {
			cache.GlobalDropMatrixBase.Set(globalDropMatrixBaseKey(server, calc.sourceCategory), *calc.newBase, 2*maxAge)
		}
		for key, aggregate := range calc.aggregates {
			aggregates[model.GlobalDropMatrixKey{SourceCategory: calc.sourceCategory, StageID: key.StageID, ItemID: key.ItemID}] = aggregate
		}
	}
	return aggregates, nil
}

// extendGlobalDropMatrixBases extends the bases of calcs in one snapshot, those whose fingerprint is still theirs.
// The others are left to rebuild (no path), with the reason.
func (s *DropMatrix) extendGlobalDropMatrixBases(
	ctx context.Context, server string, targets []*model.GlobalDropMatrixTarget, ranges map[model.GlobalMatrixStageItem]model.GlobalMatrixSecondRange,
	beforeDayNum int, calcs []*globalDropMatrixCalc, verify bool,
) error {
	elements := s.globalDropMatrixElements()
	fingerprints := s.globalMatrixFingerprints()
	return elements.RunInSnapshotTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		identity, missing, err := fingerprints.Triggers(ctx, tx, "drop_matrix_elements")
		if err != nil {
			return err
		}
		if missing != "" {
			// left to the rebuilds, which check again and aggregate every day
			for _, calc := range calcs {
				calc.reason = globalMatrixReasonTriggersMissing
			}
			return nil
		}
		bounds := []int{beforeDayNum}
		for _, calc := range calcs {
			bounds = append(bounds, calc.base.BeforeDayNum)
		}
		dayFingerprints, err := fingerprints.DayFingerprints(ctx, tx, globalMatrixKindDrop, server, lo.Uniq(bounds))
		if err != nil {
			return err
		}
		for _, calc := range calcs {
			if identity+"|"+dayFingerprints[calc.base.BeforeDayNum] != calc.base.Fingerprint {
				calc.reason = globalMatrixReasonFingerprint
				continue
			}
			if err := s.extendGlobalDropMatrixBase(ctx, tx, server, targets, ranges, beforeDayNum, identity+"|"+dayFingerprints[beforeDayNum], calc, verify); err != nil {
				return err
			}
		}
		return nil
	})
}

// extendGlobalDropMatrixBase extends the base of calc, whose fingerprint was checked in the snapshot of tx, to the
// days before beforeDayNum, with fingerprint (that of those days), and aggregates every day.
func (s *DropMatrix) extendGlobalDropMatrixBase(
	ctx context.Context, tx bun.IDB, server string, targets []*model.GlobalDropMatrixTarget,
	ranges map[model.GlobalMatrixStageItem]model.GlobalMatrixSecondRange, beforeDayNum int, fingerprint string, calc *globalDropMatrixCalc, verify bool,
) error {
	base := calc.base
	// the targets whose time range is that of the base, and the others (new, or of another time range): those are
	// aggregated over every day
	unchanged := make([]*model.GlobalDropMatrixTarget, 0, len(targets))
	changed := make([]*model.GlobalDropMatrixTarget, 0)
	changedStages := make(map[int]struct{})
	for _, target := range targets {
		key := model.GlobalMatrixStageItem{StageID: target.StageID, ItemID: target.ItemID}
		if r, ok := base.Targets[key]; ok && r == ranges[key] {
			unchanged = append(unchanged, target)
		} else {
			changed = append(changed, target)
			changedStages[target.StageID] = struct{}{}
		}
	}
	observability.GlobalMatrixChangedStages.WithLabelValues(globalMatrixKindDrop, server).Observe(float64(len(changedStages)))
	if len(changedStages) > s.Config.GlobalMatrixMaxChangedStages {
		calc.reason = globalMatrixReasonChangedStages
		return nil
	}

	elements := s.globalDropMatrixElements()
	sourceCategories := []string{calc.sourceCategory}
	// the days since the base: those settled since (before beforeDayNum) extend it, the others are recent
	settled, recent, err := elements.CalcGlobalDropMatrixPartials(ctx, tx, server, unchanged, sourceCategories,
		model.GlobalMatrixScope{SplitDayNum: beforeDayNum, FromDayNum: null.IntFrom(int64(base.BeforeDayNum))})
	if err != nil {
		return err
	}
	changedBefore, changedRecent, err := elements.CalcGlobalDropMatrixPartials(ctx, tx, server, changed, sourceCategories,
		model.GlobalMatrixScope{SplitDayNum: beforeDayNum, StageFirst: true})
	if err != nil {
		return err
	}

	// a new base: the aggregates of the base are not modified, those unchanged are shared
	newBase := &model.GlobalDropMatrixBase{
		BeforeDayNum: beforeDayNum,
		Fingerprint:  fingerprint,
		Targets:      ranges,
		Partials:     make(map[model.GlobalMatrixStageItem]*model.GlobalDropMatrixAggregate, len(base.Partials)),
		BuiltAt:      base.BuiltAt,
	}
	for _, target := range unchanged {
		key := model.GlobalMatrixStageItem{StageID: target.StageID, ItemID: target.ItemID}
		partial := base.Partials[key]
		if add, ok := settled[model.GlobalDropMatrixKey{SourceCategory: calc.sourceCategory, StageID: target.StageID, ItemID: target.ItemID}]; ok {
			if partial == nil {
				partial = add
			} else {
				partial = partial.Clone()
				partial.Add(add)
			}
		}
		if partial != nil {
			newBase.Partials[key] = partial
		}
	}
	for key, partial := range changedBefore {
		newBase.Partials[model.GlobalMatrixStageItem{StageID: key.StageID, ItemID: key.ItemID}] = partial
	}
	for key, aggregate := range changedRecent {
		recent[key] = aggregate
	}
	aggregates := addRecentGlobalDropMatrixAggregates(newBase.Partials, recent)

	calc.path, calc.reason = globalMatrixPathIncremental, globalMatrixReasonNone
	calc.newBase, calc.aggregates = newBase, aggregates
	if verify {
		if err := s.verifyGlobalDropMatrix(ctx, tx, server, targets, calc); err != nil {
			return err
		}
	}
	s.countGlobalDropMatrixPath(server, calc)
	return nil
}

// globalDropMatrixRebuild is what a rebuild of a base gives every caller.
type globalDropMatrixRebuild struct {
	ranges     map[model.GlobalMatrixStageItem]model.GlobalMatrixSecondRange
	path       string
	newBase    *model.GlobalDropMatrixBase
	aggregates map[model.GlobalMatrixStageItem]*model.GlobalDropMatrixAggregate
}

// rebuildGlobalDropMatrixBase aggregates every day of the targets for calc in a snapshot of its own, the days before
// beforeDayNum apart as a new base. A rebuild of the same server and source category already running is waited for
// instead, and its results are taken if it had the same targets, except by a refresh: the snapshot of a rebuild that
// started before the refresh may be of before the batch the refresh publishes, and nothing would then keep the
// refresh from caching it over newer matrices (cache.Set.Set does not check), so a refresh starts a rebuild of its
// own, which the calculations that need one after it share. A request may take the results of any rebuild: requests
// of a key run one after the other (cache.Set.MutexGetSet), so it shares only a refresh's, and if a refresh wrote the
// key meanwhile, MutexGetSet does not cache the request's results over it. A caller that waited for a rebuild that
// failed because the context of the caller running it was done (canceled, past its deadline) rebuilds itself,
// unless its own context is done too.
func (s *DropMatrix) rebuildGlobalDropMatrixBase(
	ctx context.Context, server string, targets []*model.GlobalDropMatrixTarget, ranges map[model.GlobalMatrixStageItem]model.GlobalMatrixSecondRange,
	beforeDayNum int, calc *globalDropMatrixCalc, verify, refresh bool,
) error {
	rebuild := func() (interface{}, error) {
		if err := s.rebuildGlobalDropMatrixBaseNow(ctx, server, targets, ranges, beforeDayNum, calc, verify); err != nil {
			return nil, err
		}
		return &globalDropMatrixRebuild{ranges: ranges, path: calc.path, newBase: calc.newBase, aggregates: calc.aggregates}, nil
	}
	key := globalDropMatrixBaseKey(server, calc.sourceCategory)
	if refresh {
		// a rebuild running is left to its callers; the rebuilds that start from now on are after the refresh
		s.incremental.rebuilds.Forget(key)
	}
	var executed bool
	v, err, _ := s.incremental.rebuilds.Do(key, func() (interface{}, error) {
		executed = true
		return rebuild()
	})
	if executed { // this caller rebuilt
		return err
	}
	if err != nil {
		if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && ctx.Err() == nil {
			_, err = rebuild()
		}
		return err
	}
	result := v.(*globalDropMatrixRebuild)
	if !sameGlobalMatrixTargetRanges(result.ranges, ranges) || result.newBase != nil && result.newBase.BeforeDayNum != beforeDayNum {
		_, err := rebuild()
		return err
	}
	// the base is cached by the caller that rebuilt it
	calc.path, calc.aggregates = result.path, result.aggregates
	return nil
}

func (s *DropMatrix) rebuildGlobalDropMatrixBaseNow(
	ctx context.Context, server string, targets []*model.GlobalDropMatrixTarget, ranges map[model.GlobalMatrixStageItem]model.GlobalMatrixSecondRange,
	beforeDayNum int, calc *globalDropMatrixCalc, verify bool,
) error {
	elements := s.globalDropMatrixElements()
	fingerprints := s.globalMatrixFingerprints()
	sourceCategories := []string{calc.sourceCategory}
	var (
		path       string
		newBase    *model.GlobalDropMatrixBase
		aggregates map[model.GlobalMatrixStageItem]*model.GlobalDropMatrixAggregate
	)
	err := elements.RunInSnapshotTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		path, newBase, aggregates = "", nil, nil
		identity, missing, err := fingerprints.Triggers(ctx, tx, "drop_matrix_elements")
		if err != nil {
			return err
		}
		if missing != "" {
			// writes may go unversioned: no base can be trusted, of any process. Bases of this process are dropped
			// (those of the others drop theirs as they find the triggers changed, or missing)
			log.Warn().Str("evt.name", "globalmatrix.triggers_missing").Str("server", server).Str("problem", missing).
				Msg("the day versions of the drop matrix elements cannot be relied on: aggregating every day, without bases")
			deleteGlobalDropMatrixBases(server)
			reference, err := elements.CalcGlobalDropMatrixAggregates(ctx, tx, server, targets, sourceCategories)
			if err != nil {
				return err
			}
			path, aggregates = globalMatrixPathFull, globalDropMatrixAggregatesOf(reference, calc.sourceCategory)
			return nil
		}
		dayFingerprints, err := fingerprints.DayFingerprints(ctx, tx, globalMatrixKindDrop, server, []int{beforeDayNum})
		if err != nil {
			return err
		}
		before, after, err := elements.CalcGlobalDropMatrixPartials(ctx, tx, server, targets, sourceCategories,
			model.GlobalMatrixScope{SplitDayNum: beforeDayNum})
		if err != nil {
			return err
		}
		newBase = &model.GlobalDropMatrixBase{
			BeforeDayNum: beforeDayNum,
			Fingerprint:  identity + "|" + dayFingerprints[beforeDayNum],
			Targets:      ranges,
			Partials:     globalDropMatrixAggregatesOf(before, calc.sourceCategory),
			BuiltAt:      util.Now(),
		}
		path, aggregates = globalMatrixPathRebuild, addRecentGlobalDropMatrixAggregates(newBase.Partials, after)
		if verify {
			calc.path, calc.newBase, calc.aggregates = path, newBase, aggregates
			if err := s.verifyGlobalDropMatrix(ctx, tx, server, targets, calc); err != nil {
				return err
			}
			newBase, aggregates = calc.newBase, calc.aggregates
		}
		return nil
	})
	if err != nil {
		calc.path, calc.newBase, calc.aggregates = "", nil, nil
		return err
	}
	if path == globalMatrixPathFull {
		calc.reason = globalMatrixReasonTriggersMissing
	}
	calc.path, calc.newBase, calc.aggregates = path, newBase, aggregates
	s.countGlobalDropMatrixPath(server, calc)
	return nil
}

func (s *DropMatrix) countGlobalDropMatrixPath(server string, calc *globalDropMatrixCalc) {
	observability.GlobalMatrixRefreshPaths.WithLabelValues(globalMatrixKindDrop, server, calc.sourceCategory, calc.path, calc.reason).Inc()
}

// verifyGlobalDropMatrix aggregates the targets for calc as aggregateGlobalDropMatrices does, in the snapshot of tx,
// and compares: the raw aggregates per target, and the matrices assembled from them. If they differ, the mismatch is
// counted and logged, the base dropped, and calc gets the reference aggregates. In verify mode, calc gets them
// anyway.
func (s *DropMatrix) verifyGlobalDropMatrix(ctx context.Context, tx bun.IDB, server string, targets []*model.GlobalDropMatrixTarget, calc *globalDropMatrixCalc) error {
	sourceCategories := []string{calc.sourceCategory}
	referenceAggregates, err := s.globalDropMatrixElements().CalcGlobalDropMatrixAggregates(ctx, tx, server, targets, sourceCategories)
	if err != nil {
		return err
	}
	reference := globalDropMatrixAggregatesOf(referenceAggregates, calc.sourceCategory)
	mismatches := diffGlobalDropMatrixAggregates(targets, calc.aggregates, reference)
	if len(mismatches) == 0 {
		gotLines := globalDropMatrixResultLines(assembleGlobalDropMatrices(targets, globalDropMatrixKeyed(calc.aggregates, calc.sourceCategory), sourceCategories)[calc.sourceCategory])
		wantLines := globalDropMatrixResultLines(assembleGlobalDropMatrices(targets, referenceAggregates, sourceCategories)[calc.sourceCategory])
		missing, extra := lo.Difference(wantLines, gotLines)
		for _, line := range missing {
			mismatches = append(mismatches, "matrix without "+line)
		}
		for _, line := range extra {
			mismatches = append(mismatches, "matrix with "+line)
		}
	}
	if len(mismatches) > 0 {
		observability.GlobalMatrixVerifyMismatches.WithLabelValues(globalMatrixKindDrop, server, calc.sourceCategory).Inc()
		log.Error().Str("evt.name", "globalmatrix.verify_mismatch").Str("server", server).Str("sourceCategory", calc.sourceCategory).
			Str("path", calc.path).Int("mismatches", len(mismatches)).Strs("keys", mismatches[:min(len(mismatches), maxLoggedMismatches)]).
			Msg("the incremental global drop matrix differs from the reference: serving the reference, dropping the base")
		calc.newBase = nil
		_ = cache.GlobalDropMatrixBase.Delete(globalDropMatrixBaseKey(server, calc.sourceCategory))
	}
	if len(mismatches) > 0 || s.Config.GlobalMatrixIncremental == appconfig.GlobalMatrixIncrementalVerify {
		calc.aggregates = reference
	}
	return nil
}

// deleteGlobalDropMatrixBases drops the bases of server of every source category.
func deleteGlobalDropMatrixBases(server string) {
	for _, sourceCategory := range []string{constant.SourceCategoryAll, constant.SourceCategoryAutomated, constant.SourceCategoryManual} {
		deleteCached(cache.GlobalDropMatrixBase, globalDropMatrixBaseKey(server, sourceCategory))
	}
}

// globalMatrixTargetRanges returns the time ranges of targets as the aggregation compares them, in whole seconds.
func globalMatrixTargetRanges(targets []*model.GlobalDropMatrixTarget) map[model.GlobalMatrixStageItem]model.GlobalMatrixSecondRange {
	ranges := make(map[model.GlobalMatrixStageItem]model.GlobalMatrixSecondRange, len(targets))
	for _, target := range targets {
		ranges[model.GlobalMatrixStageItem{StageID: target.StageID, ItemID: target.ItemID}] = model.GlobalMatrixSecondRange{
			Start: target.TimeRange.StartTime.Unix(), End: target.TimeRange.EndTime.Unix(),
		}
	}
	return ranges
}

func sameGlobalMatrixTargetRanges(a, b map[model.GlobalMatrixStageItem]model.GlobalMatrixSecondRange) bool {
	if len(a) != len(b) {
		return false
	}
	for key, r := range a {
		if other, ok := b[key]; !ok || other != r {
			return false
		}
	}
	return true
}

// globalDropMatrixAggregatesOf returns the aggregates of sourceCategory in aggregates, by stage and item.
func globalDropMatrixAggregatesOf(
	aggregates map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate, sourceCategory string,
) map[model.GlobalMatrixStageItem]*model.GlobalDropMatrixAggregate {
	of := make(map[model.GlobalMatrixStageItem]*model.GlobalDropMatrixAggregate, len(aggregates))
	for key, aggregate := range aggregates {
		if key.SourceCategory == sourceCategory {
			of[model.GlobalMatrixStageItem{StageID: key.StageID, ItemID: key.ItemID}] = aggregate
		}
	}
	return of
}

// globalDropMatrixKeyed is the inverse of globalDropMatrixAggregatesOf.
func globalDropMatrixKeyed(
	aggregates map[model.GlobalMatrixStageItem]*model.GlobalDropMatrixAggregate, sourceCategory string,
) map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate {
	keyed := make(map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate, len(aggregates))
	for key, aggregate := range aggregates {
		keyed[model.GlobalDropMatrixKey{SourceCategory: sourceCategory, StageID: key.StageID, ItemID: key.ItemID}] = aggregate
	}
	return keyed
}

// addRecentGlobalDropMatrixAggregates returns base Add recent per target, with new aggregates where both have one:
// those of base are not modified.
func addRecentGlobalDropMatrixAggregates(
	base map[model.GlobalMatrixStageItem]*model.GlobalDropMatrixAggregate, recent map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate,
) map[model.GlobalMatrixStageItem]*model.GlobalDropMatrixAggregate {
	aggregates := make(map[model.GlobalMatrixStageItem]*model.GlobalDropMatrixAggregate, len(base)+len(recent))
	for key, aggregate := range base {
		aggregates[key] = aggregate
	}
	for key, add := range recent {
		stageItem := model.GlobalMatrixStageItem{StageID: key.StageID, ItemID: key.ItemID}
		if aggregate, ok := aggregates[stageItem]; ok {
			aggregate = aggregate.Clone()
			aggregate.Add(add)
			aggregates[stageItem] = aggregate
		} else {
			aggregates[stageItem] = add
		}
	}
	return aggregates
}

// diffGlobalDropMatrixAggregates compares the aggregates of the targets, an aggregate with no valid part and no
// bucket being none, and describes those that differ.
func diffGlobalDropMatrixAggregates(
	targets []*model.GlobalDropMatrixTarget, got, want map[model.GlobalMatrixStageItem]*model.GlobalDropMatrixAggregate,
) []string {
	mismatches := make([]string, 0)
	for _, target := range targets {
		key := model.GlobalMatrixStageItem{StageID: target.StageID, ItemID: target.ItemID}
		if g, w := describeGlobalDropMatrixAggregate(got[key]), describeGlobalDropMatrixAggregate(want[key]); g != w {
			mismatches = append(mismatches, fmt.Sprintf("%d|%d: %s, want %s", key.StageID, key.ItemID, g, w))
		}
	}
	return mismatches
}

func describeGlobalDropMatrixAggregate(aggregate *model.GlobalDropMatrixAggregate) string {
	if aggregate == nil || !aggregate.Times.Valid && !aggregate.Quantity.Valid && len(aggregate.QuantityBuckets) == 0 {
		return "none"
	}
	quantities := lo.Keys(aggregate.QuantityBuckets)
	sort.Ints(quantities)
	var b strings.Builder
	fmt.Fprintf(&b, "times %s quantity %s buckets", formatNullInt(aggregate.Times), formatNullInt(aggregate.Quantity))
	for _, quantity := range quantities {
		fmt.Fprintf(&b, " %d:%d", quantity, aggregate.QuantityBuckets[quantity])
	}
	return b.String()
}

func formatNullInt(i null.Int) string {
	if !i.Valid {
		return "null"
	}
	return fmt.Sprint(i.Int64)
}

// globalDropMatrixResultLines describes the elements of a global drop matrix, one line each.
func globalDropMatrixResultLines(result *model.DropMatrixQueryResult) []string {
	lines := make([]string, 0, len(result.Matrix))
	for _, el := range result.Matrix {
		lines = append(lines, fmt.Sprintf("%d|%d|%d|%d|%d|%d|%v", el.StageID, el.ItemID,
			el.TimeRange.StartTime.UnixMilli(), el.TimeRange.EndTime.UnixMilli(), el.Times, el.Quantity, el.StdDev))
	}
	return lines
}
