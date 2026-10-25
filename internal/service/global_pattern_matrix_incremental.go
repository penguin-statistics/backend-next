package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"exusiai.dev/gommon/constant"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/util"
)

// The incremental global pattern matrices (GlobalMatrixIncremental, #860): see global_matrix_incremental.go. A base
// is kept per server and source category, of the aggregates of every target stage: the targets are the stages, and
// a changed target is a changed stage.

// globalPatternMatrixStore reads the pattern matrix elements for the incremental global pattern matrices
// (PatternMatrixElement).
type globalPatternMatrixStore interface {
	// RunInSnapshotTx runs fn in a read-only snapshot transaction, the elements locked from before the snapshot is
	// taken, as globalDropMatrixStore's does.
	RunInSnapshotTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error
	CalcGlobalPatternMatrixAggregates(
		ctx context.Context, db bun.IDB, server string, targets []*model.GlobalPatternMatrixTarget, sourceCategories []string,
	) (map[string]*model.GlobalPatternMatrixAggregates, error)
	CalcGlobalPatternMatrixPartials(
		ctx context.Context, db bun.IDB, server string, targets []*model.GlobalPatternMatrixTarget, sourceCategories []string,
		scope model.GlobalMatrixScope,
	) (before, after map[string]*model.GlobalPatternMatrixAggregates, err error)
}

// globalPatternMatrixCalc is the calculation of the global pattern matrix of a source category.
type globalPatternMatrixCalc struct {
	sourceCategory string
	// base is the cached base, if any
	base *model.GlobalPatternMatrixBase
	// path and reason, once decided (path is empty until then; reason may be set before, as the reason to rebuild)
	path, reason string
	// newBase is the base to cache once the calculation succeeded, if any
	newBase *model.GlobalPatternMatrixBase
	// aggregates are the aggregates of every day of the targets
	aggregates *model.GlobalPatternMatrixAggregates
}

func globalPatternMatrixBaseKey(server, sourceCategory string) string {
	return server + constant.CacheSep + sourceCategory
}

func (s *PatternMatrix) globalMatrixFingerprints() GlobalMatrixFingerprints {
	if s.GlobalMatrixFingerprints != nil {
		return s.GlobalMatrixFingerprints
	}
	return dayVersionFingerprints{}
}

func (s *PatternMatrix) globalPatternMatrixElements() globalPatternMatrixStore {
	if s.globalPatternMatrixStore != nil {
		return s.globalPatternMatrixStore
	}
	return s.PatternMatrixElementService
}

// aggregateGlobalPatternMatricesIncrementally aggregates the elements of the targets of server over every day for
// every source category, as aggregateGlobalPatternMatrices does, from the cached bases, as
// DropMatrix.aggregateGlobalDropMatricesIncrementally does for the drop matrices: for each source category, it
// rebuilds a base without a usable one, extends it otherwise (aggregating every day of the stages whose target time
// range changed), aggregates every day without a base if the triggers of the day versions are missing or not firing
// always, and verifies as configured. refresh is for a refresh (see globalMatrixIncremental.shareRebuild).
func (s *PatternMatrix) aggregateGlobalPatternMatricesIncrementally(
	ctx context.Context, server string, targets []*model.GlobalPatternMatrixTarget, sourceCategories []string, refresh bool,
) (map[string]*model.GlobalPatternMatrixAggregates, error) {
	now := util.Now()
	// the days before yesterday: the worker rewrites yesterday when the day changes, maybe after this calculation
	beforeDayNum := util.GetDayNum(&now, server) - 1
	verify := s.incremental.verifies(s.Config, server)
	maxAge := s.incremental.maxAge(s.Config.GlobalMatrixBaseMaxAge)
	ranges := globalPatternMatrixTargetRanges(targets)

	calcs := make([]*globalPatternMatrixCalc, 0, len(sourceCategories))
	extensible := make([]*globalPatternMatrixCalc, 0, len(sourceCategories))
	for _, sourceCategory := range sourceCategories {
		calc := &globalPatternMatrixCalc{sourceCategory: sourceCategory}
		calcs = append(calcs, calc)
		var base model.GlobalPatternMatrixBase
		if err := cache.GlobalPatternMatrixBase.Get(globalPatternMatrixBaseKey(server, sourceCategory), &base); err != nil {
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
		if err := s.extendGlobalPatternMatrixBases(ctx, server, targets, ranges, beforeDayNum, extensible, verify); err != nil {
			return nil, err
		}
	}
	for _, calc := range calcs {
		if calc.path == "" {
			if err := s.rebuildGlobalPatternMatrixBase(ctx, server, targets, ranges, beforeDayNum, calc, verify, refresh); err != nil {
				return nil, err
			}
		}
	}

	aggregates := make(map[string]*model.GlobalPatternMatrixAggregates, len(calcs))
	for _, calc := range calcs {
		if calc.newBase != nil {
			cache.GlobalPatternMatrixBase.Set(globalPatternMatrixBaseKey(server, calc.sourceCategory), *calc.newBase, 2*maxAge)
		}
		aggregates[calc.sourceCategory] = calc.aggregates
	}
	return aggregates, nil
}

// extendGlobalPatternMatrixBases extends the bases of calcs in one snapshot, those whose fingerprint is still theirs.
// The others are left to rebuild (no path), with the reason.
func (s *PatternMatrix) extendGlobalPatternMatrixBases(
	ctx context.Context, server string, targets []*model.GlobalPatternMatrixTarget, ranges map[int]model.GlobalMatrixSecondRange,
	beforeDayNum int, calcs []*globalPatternMatrixCalc, verify bool,
) error {
	elements := s.globalPatternMatrixElements()
	fingerprints := s.globalMatrixFingerprints()
	return elements.RunInSnapshotTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		identity, missing, err := fingerprints.Triggers(ctx, tx, "pattern_matrix_elements")
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
		dayFingerprints, err := fingerprints.DayFingerprints(ctx, tx, globalMatrixKindPattern, server, lo.Uniq(bounds))
		if err != nil {
			return err
		}
		for _, calc := range calcs {
			if identity+"|"+dayFingerprints[calc.base.BeforeDayNum] != calc.base.Fingerprint {
				calc.reason = globalMatrixReasonFingerprint
				continue
			}
			if err := s.extendGlobalPatternMatrixBase(ctx, tx, server, targets, ranges, beforeDayNum, identity+"|"+dayFingerprints[beforeDayNum], calc, verify); err != nil {
				return err
			}
		}
		return nil
	})
}

// extendGlobalPatternMatrixBase extends the base of calc, whose fingerprint was checked in the snapshot of tx, to the
// days before beforeDayNum, with fingerprint (that of those days), and aggregates every day.
func (s *PatternMatrix) extendGlobalPatternMatrixBase(
	ctx context.Context, tx bun.IDB, server string, targets []*model.GlobalPatternMatrixTarget,
	ranges map[int]model.GlobalMatrixSecondRange, beforeDayNum int, fingerprint string, calc *globalPatternMatrixCalc, verify bool,
) error {
	base := calc.base
	// the target stages whose time range is that of the base, and the others (new, or of another time range): those
	// are aggregated over every day
	unchanged := make([]*model.GlobalPatternMatrixTarget, 0, len(targets))
	changed := make([]*model.GlobalPatternMatrixTarget, 0)
	for _, target := range targets {
		if r, ok := base.Targets[target.StageID]; ok && r == ranges[target.StageID] {
			unchanged = append(unchanged, target)
		} else {
			changed = append(changed, target)
		}
	}
	observability.GlobalMatrixChangedStages.WithLabelValues(globalMatrixKindPattern, server).Observe(float64(len(changed)))
	if len(changed) > s.Config.GlobalMatrixMaxChangedStages {
		calc.reason = globalMatrixReasonChangedStages
		return nil
	}

	elements := s.globalPatternMatrixElements()
	sourceCategories := []string{calc.sourceCategory}
	// the days since the base: those settled since (before beforeDayNum) extend it, the others are recent
	settled, recent, err := elements.CalcGlobalPatternMatrixPartials(ctx, tx, server, unchanged, sourceCategories,
		model.GlobalMatrixScope{SplitDayNum: beforeDayNum, FromDayNum: null.IntFrom(int64(base.BeforeDayNum))})
	if err != nil {
		return err
	}
	changedBefore, changedRecent, err := elements.CalcGlobalPatternMatrixPartials(ctx, tx, server, changed, sourceCategories,
		model.GlobalMatrixScope{SplitDayNum: beforeDayNum, StageFirst: true})
	if err != nil {
		return err
	}

	// a new base: the aggregates of the base are not modified, the quantities of the stages unchanged are shared;
	// those of the stages that are no longer targets are left out
	partials := addGlobalPatternMatrixAggregates(globalPatternMatrixAggregatesOfStages(&base.Partials, unchanged), settled[calc.sourceCategory])
	partials = addGlobalPatternMatrixAggregates(partials, changedBefore[calc.sourceCategory])
	newBase := &model.GlobalPatternMatrixBase{
		BeforeDayNum: beforeDayNum,
		Fingerprint:  fingerprint,
		Targets:      ranges,
		Partials:     *partials,
		BuiltAt:      base.BuiltAt,
	}
	aggregates := addGlobalPatternMatrixAggregates(partials,
		addGlobalPatternMatrixAggregates(recent[calc.sourceCategory], changedRecent[calc.sourceCategory]))

	calc.path, calc.reason = globalMatrixPathIncremental, globalMatrixReasonNone
	calc.newBase, calc.aggregates = newBase, aggregates
	if verify {
		if err := s.verifyGlobalPatternMatrix(ctx, tx, server, targets, calc); err != nil {
			return err
		}
	}
	s.countGlobalPatternMatrixPath(server, calc)
	return nil
}

// globalPatternMatrixRebuild is what a rebuild of a base gives every caller.
type globalPatternMatrixRebuild struct {
	ranges     map[int]model.GlobalMatrixSecondRange
	path       string
	newBase    *model.GlobalPatternMatrixBase
	aggregates *model.GlobalPatternMatrixAggregates
}

// rebuildGlobalPatternMatrixBase aggregates every day of the targets for calc in a snapshot of its own, the days
// before beforeDayNum apart as a new base. A rebuild of the same server and source category already running is
// waited for instead, and its results are taken if it had the same targets, except by a refresh (see
// globalMatrixIncremental.shareRebuild). A request may share the rebuild of another request: the cached global pattern
// matrices of a source category, with all and with the top patterns, have a base in common.
func (s *PatternMatrix) rebuildGlobalPatternMatrixBase(
	ctx context.Context, server string, targets []*model.GlobalPatternMatrixTarget, ranges map[int]model.GlobalMatrixSecondRange,
	beforeDayNum int, calc *globalPatternMatrixCalc, verify, refresh bool,
) error {
	rebuild := func() (interface{}, error) {
		if err := s.rebuildGlobalPatternMatrixBaseNow(ctx, server, targets, ranges, beforeDayNum, calc, verify); err != nil {
			return nil, err
		}
		return &globalPatternMatrixRebuild{ranges: ranges, path: calc.path, newBase: calc.newBase, aggregates: calc.aggregates}, nil
	}
	usable := func(shared interface{}) bool {
		result := shared.(*globalPatternMatrixRebuild)
		return sameGlobalMatrixTargetRanges(result.ranges, ranges) && (result.newBase == nil || result.newBase.BeforeDayNum == beforeDayNum)
	}
	shared, err := s.incremental.shareRebuild(ctx, globalPatternMatrixBaseKey(server, calc.sourceCategory), refresh, rebuild, usable)
	if err != nil || shared == nil {
		return err
	}
	// the base is cached by the caller that rebuilt it
	result := shared.(*globalPatternMatrixRebuild)
	calc.path, calc.aggregates = result.path, result.aggregates
	return nil
}

func (s *PatternMatrix) rebuildGlobalPatternMatrixBaseNow(
	ctx context.Context, server string, targets []*model.GlobalPatternMatrixTarget, ranges map[int]model.GlobalMatrixSecondRange,
	beforeDayNum int, calc *globalPatternMatrixCalc, verify bool,
) error {
	elements := s.globalPatternMatrixElements()
	fingerprints := s.globalMatrixFingerprints()
	sourceCategories := []string{calc.sourceCategory}
	var (
		path       string
		newBase    *model.GlobalPatternMatrixBase
		aggregates *model.GlobalPatternMatrixAggregates
	)
	err := elements.RunInSnapshotTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		path, newBase, aggregates = "", nil, nil
		identity, missing, err := fingerprints.Triggers(ctx, tx, "pattern_matrix_elements")
		if err != nil {
			return err
		}
		if missing != "" {
			// writes may go unversioned: no base can be trusted, of any process. Bases of this process are dropped
			// (those of the others drop theirs as they find the triggers changed, or missing)
			log.Warn().Str("evt.name", "globalmatrix.triggers_missing").Str("server", server).Str("problem", missing).
				Msg("the day versions of the pattern matrix elements cannot be relied on: aggregating every day, without bases")
			deleteGlobalPatternMatrixBases(server)
			reference, err := elements.CalcGlobalPatternMatrixAggregates(ctx, tx, server, targets, sourceCategories)
			if err != nil {
				return err
			}
			path, aggregates = globalMatrixPathFull, addGlobalPatternMatrixAggregates(reference[calc.sourceCategory], nil)
			return nil
		}
		dayFingerprints, err := fingerprints.DayFingerprints(ctx, tx, globalMatrixKindPattern, server, []int{beforeDayNum})
		if err != nil {
			return err
		}
		before, after, err := elements.CalcGlobalPatternMatrixPartials(ctx, tx, server, targets, sourceCategories,
			model.GlobalMatrixScope{SplitDayNum: beforeDayNum})
		if err != nil {
			return err
		}
		partials := addGlobalPatternMatrixAggregates(before[calc.sourceCategory], nil)
		newBase = &model.GlobalPatternMatrixBase{
			BeforeDayNum: beforeDayNum,
			Fingerprint:  identity + "|" + dayFingerprints[beforeDayNum],
			Targets:      ranges,
			Partials:     *partials,
			BuiltAt:      util.Now(),
		}
		path, aggregates = globalMatrixPathRebuild, addGlobalPatternMatrixAggregates(partials, after[calc.sourceCategory])
		if verify {
			calc.path, calc.newBase, calc.aggregates = path, newBase, aggregates
			if err := s.verifyGlobalPatternMatrix(ctx, tx, server, targets, calc); err != nil {
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
	s.countGlobalPatternMatrixPath(server, calc)
	return nil
}

func (s *PatternMatrix) countGlobalPatternMatrixPath(server string, calc *globalPatternMatrixCalc) {
	observability.GlobalMatrixRefreshPaths.WithLabelValues(globalMatrixKindPattern, server, calc.sourceCategory, calc.path, calc.reason).Inc()
}

// verifyGlobalPatternMatrix aggregates the targets for calc as aggregateGlobalPatternMatrices does, in the snapshot of
// tx, and compares: the raw aggregates per target stage, and the matrices assembled from them. If they differ, the
// mismatch is counted and logged, the base dropped, and calc gets the reference aggregates. In verify mode, calc gets
// them anyway.
func (s *PatternMatrix) verifyGlobalPatternMatrix(
	ctx context.Context, tx bun.IDB, server string, targets []*model.GlobalPatternMatrixTarget, calc *globalPatternMatrixCalc,
) error {
	sourceCategories := []string{calc.sourceCategory}
	referenceAggregates, err := s.globalPatternMatrixElements().CalcGlobalPatternMatrixAggregates(ctx, tx, server, targets, sourceCategories)
	if err != nil {
		return err
	}
	reference := referenceAggregates[calc.sourceCategory]
	mismatches := diffGlobalPatternMatrixAggregates(targets, calc.aggregates, reference)
	if len(mismatches) == 0 {
		gotLines := globalPatternMatrixAssembledLines(server, targets, calc.sourceCategory, calc.aggregates)
		wantLines := globalPatternMatrixAssembledLines(server, targets, calc.sourceCategory, reference)
		missing, extra := lo.Difference(wantLines, gotLines)
		for _, line := range missing {
			mismatches = append(mismatches, "matrix without "+line)
		}
		for _, line := range extra {
			mismatches = append(mismatches, "matrix with "+line)
		}
	}
	if len(mismatches) > 0 {
		observability.GlobalMatrixVerifyMismatches.WithLabelValues(globalMatrixKindPattern, server, calc.sourceCategory).Inc()
		log.Error().Str("evt.name", "globalmatrix.verify_mismatch").Str("kind", globalMatrixKindPattern).Str("server", server).
			Str("sourceCategory", calc.sourceCategory).Str("path", calc.path).Int("mismatches", len(mismatches)).
			Strs("keys", mismatches[:min(len(mismatches), maxLoggedMismatches)]).
			Msg("the incremental global pattern matrix differs from the reference: serving the reference, dropping the base")
		calc.newBase = nil
		_ = cache.GlobalPatternMatrixBase.Delete(globalPatternMatrixBaseKey(server, calc.sourceCategory))
	}
	if len(mismatches) > 0 || s.Config.GlobalMatrixIncremental == appconfig.GlobalMatrixIncrementalVerify {
		calc.aggregates = reference
	}
	return nil
}

// deleteGlobalPatternMatrixBases drops the bases of server of every source category.
func deleteGlobalPatternMatrixBases(server string) {
	for _, sourceCategory := range []string{constant.SourceCategoryAll, constant.SourceCategoryAutomated, constant.SourceCategoryManual} {
		deleteCached(cache.GlobalPatternMatrixBase, globalPatternMatrixBaseKey(server, sourceCategory))
	}
}

// globalPatternMatrixTargetRanges returns the time ranges of targets by stage as the aggregation compares them, in
// whole seconds.
func globalPatternMatrixTargetRanges(targets []*model.GlobalPatternMatrixTarget) map[int]model.GlobalMatrixSecondRange {
	ranges := make(map[int]model.GlobalMatrixSecondRange, len(targets))
	for _, target := range targets {
		ranges[target.StageID] = model.GlobalMatrixSecondRange{Start: target.TimeRange.StartTime.Unix(), End: target.TimeRange.EndTime.Unix()}
	}
	return ranges
}

// globalPatternMatrixAggregatesOfStages returns the aggregates of the stages of targets in aggregates, sharing their
// quantities.
func globalPatternMatrixAggregatesOfStages(
	aggregates *model.GlobalPatternMatrixAggregates, targets []*model.GlobalPatternMatrixTarget,
) *model.GlobalPatternMatrixAggregates {
	of := &model.GlobalPatternMatrixAggregates{
		TimesByStageID:      make(map[int]int, len(targets)),
		QuantitiesByStageID: make(map[int]map[int]int, len(targets)),
	}
	for _, target := range targets {
		if times, ok := aggregates.TimesByStageID[target.StageID]; ok {
			of.TimesByStageID[target.StageID] = times
		}
		if quantities, ok := aggregates.QuantitiesByStageID[target.StageID]; ok {
			of.QuantitiesByStageID[target.StageID] = quantities
		}
	}
	return of
}

// addGlobalPatternMatrixAggregates returns a Add b (either may be nil, for none) in new maps, without modifying
// either: the quantities of a stage of only one of them are shared with it, new where both have some.
func addGlobalPatternMatrixAggregates(a, b *model.GlobalPatternMatrixAggregates) *model.GlobalPatternMatrixAggregates {
	if a == nil {
		a = &model.GlobalPatternMatrixAggregates{}
	}
	if b == nil {
		b = &model.GlobalPatternMatrixAggregates{}
	}
	sum := &model.GlobalPatternMatrixAggregates{
		TimesByStageID:      make(map[int]int, len(a.TimesByStageID)+len(b.TimesByStageID)),
		QuantitiesByStageID: make(map[int]map[int]int, len(a.QuantitiesByStageID)+len(b.QuantitiesByStageID)),
	}
	for stageId, times := range a.TimesByStageID {
		sum.TimesByStageID[stageId] = times
	}
	for stageId, times := range b.TimesByStageID {
		sum.TimesByStageID[stageId] += times
	}
	for stageId, quantities := range a.QuantitiesByStageID {
		sum.QuantitiesByStageID[stageId] = quantities
	}
	for stageId, quantities := range b.QuantitiesByStageID {
		mine, ok := sum.QuantitiesByStageID[stageId]
		if !ok {
			sum.QuantitiesByStageID[stageId] = quantities
			continue
		}
		added := make(map[int]int, len(mine)+len(quantities))
		for patternId, quantity := range mine {
			added[patternId] = quantity
		}
		for patternId, quantity := range quantities {
			added[patternId] += quantity
		}
		sum.QuantitiesByStageID[stageId] = added
	}
	return sum
}

// diffGlobalPatternMatrixAggregates compares the aggregates of the target stages, a stage with no times and no
// quantity being none, and describes those that differ.
func diffGlobalPatternMatrixAggregates(targets []*model.GlobalPatternMatrixTarget, got, want *model.GlobalPatternMatrixAggregates) []string {
	mismatches := make([]string, 0)
	for _, target := range targets {
		if g, w := describeGlobalPatternMatrixStage(got, target.StageID), describeGlobalPatternMatrixStage(want, target.StageID); g != w {
			mismatches = append(mismatches, fmt.Sprintf("%d: %s, want %s", target.StageID, g, w))
		}
	}
	return mismatches
}

func describeGlobalPatternMatrixStage(aggregates *model.GlobalPatternMatrixAggregates, stageId int) string {
	if aggregates == nil {
		return "none"
	}
	times, ok := aggregates.TimesByStageID[stageId]
	quantities := aggregates.QuantitiesByStageID[stageId]
	if !ok && len(quantities) == 0 {
		return "none"
	}
	patternIds := lo.Keys(quantities)
	sort.Ints(patternIds)
	var b strings.Builder
	fmt.Fprintf(&b, "times %s patterns", formatNullInt(null.NewInt(int64(times), ok)))
	for _, patternId := range patternIds {
		fmt.Fprintf(&b, " %d:%d", patternId, quantities[patternId])
	}
	return b.String()
}

// globalPatternMatrixAssembledLines describes the elements of the global pattern matrix of sourceCategory assembled
// from aggregates, one line each, or why it cannot be assembled.
func globalPatternMatrixAssembledLines(
	server string, targets []*model.GlobalPatternMatrixTarget, sourceCategory string, aggregates *model.GlobalPatternMatrixAggregates,
) []string {
	results, err := assembleGlobalPatternMatrices(server, targets, map[string]*model.GlobalPatternMatrixAggregates{sourceCategory: aggregates}, []string{sourceCategory})
	if err != nil {
		return []string{"error " + err.Error()}
	}
	lines := make([]string, 0, len(results[sourceCategory].PatternMatrix))
	for _, el := range results[sourceCategory].PatternMatrix {
		lines = append(lines, fmt.Sprintf("%d|%d|%d|%d|%d|%d", el.StageID, el.PatternID,
			el.TimeRange.StartTime.UnixMilli(), el.TimeRange.EndTime.UnixMilli(), el.Times, el.Quantity))
	}
	return lines
}
