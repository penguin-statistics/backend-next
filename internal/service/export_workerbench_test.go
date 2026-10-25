//go:build workerbench

package service

import (
	"context"
	"sort"
	"time"

	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
)

// Exposes the global matrix calculations to the external globalmatrixbench harness.

func (s *DropMatrix) CalcGlobalDropMatrixForBench(ctx context.Context, server, sourceCategory string) (*model.DropMatrixQueryResult, error) {
	return s.calcGlobalDropMatrixUncached(ctx, server, sourceCategory)
}

func (s *PatternMatrix) CalcGlobalPatternMatrixForBench(ctx context.Context, server, sourceCategory string) (*model.PatternMatrixQueryResult, error) {
	return s.calcGlobalPatternMatrix(ctx, server, sourceCategory)
}

func (s *DropMatrix) CalcGlobalDropMatricesForBench(ctx context.Context, server string, sourceCategories []string) (map[string]*model.DropMatrixQueryResult, error) {
	return s.calcGlobalDropMatrices(ctx, server, sourceCategories)
}

func (s *PatternMatrix) CalcGlobalPatternMatricesForBench(ctx context.Context, server string, sourceCategories []string) (map[string]*model.PatternMatrixQueryResult, error) {
	return s.calcGlobalPatternMatrices(ctx, server, sourceCategories)
}

// constantGlobalMatrixFingerprints stands in for the day versions on a database without them (a shared restore that
// cannot take the migration): the triggers are always there, and no day is ever written, so every base is extended.
type constantGlobalMatrixFingerprints struct{}

func (constantGlobalMatrixFingerprints) Triggers(context.Context, bun.IDB, string) (identity, missing string, err error) {
	return "constant", "", nil
}

func (constantGlobalMatrixFingerprints) DayFingerprints(_ context.Context, _ bun.IDB, _, _ string, bounds []int) (map[int]string, error) {
	fingerprints := make(map[int]string, len(bounds))
	for _, bound := range bounds {
		fingerprints[bound] = "constant"
	}
	return fingerprints, nil
}

// SetGlobalMatrixIncrementalForBench sets the mode of the global drop matrices, with constant fingerprints and bases
// that never expire, and drops the bases.
func (s *DropMatrix) SetGlobalMatrixIncrementalForBench(mode appconfig.GlobalMatrixIncrementalMode) {
	s.Config.GlobalMatrixIncremental = mode
	s.Config.GlobalMatrixBaseMaxAge = 100 * 365 * 24 * time.Hour
	s.GlobalMatrixFingerprints = constantGlobalMatrixFingerprints{}
	_ = cache.GlobalDropMatrixBase.Flush()
}

// CalcGlobalDropMatrixStageFirstForBench aggregates every day of the targets of the first n stages (by id) of server
// in a source category as an incremental calculation recomputes changed targets, and returns how many targets.
func (s *DropMatrix) CalcGlobalDropMatrixStageFirstForBench(ctx context.Context, server, sourceCategory string, n int, splitDayNum int) (int, error) {
	targets, err := s.globalDropMatrixTargets(ctx, server)
	if err != nil {
		return 0, err
	}
	stages := make([]int, 0)
	seen := make(map[int]bool)
	for _, target := range targets {
		if !seen[target.StageID] {
			seen[target.StageID] = true
			stages = append(stages, target.StageID)
		}
	}
	sort.Ints(stages)
	stages = stages[:min(n, len(stages))]
	chosen := make([]*model.GlobalDropMatrixTarget, 0)
	for _, target := range targets {
		if sort.SearchInts(stages, target.StageID) < len(stages) && stages[sort.SearchInts(stages, target.StageID)] == target.StageID {
			chosen = append(chosen, target)
		}
	}
	_, _, err = s.DropMatrixElementService.CalcGlobalDropMatrixPartials(ctx, s.DropMatrixElementService.DB(), server, chosen, []string{sourceCategory},
		model.GlobalMatrixScope{SplitDayNum: splitDayNum, StageFirst: true})
	return len(chosen), err
}
