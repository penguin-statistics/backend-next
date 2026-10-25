package repo

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model"
)

// insertGlobalPatternMatrixFixture inserts random pattern matrix elements of stages 1 to globalMatrixStageCount + 1
// (the last one has no target) and patterns 1 to 4 on CN and US for every source category: per stage and day, one or
// two calculations, with the same or with different times (possibly 0), each with elements of quantity 0 too, with
// the windows of insertGlobalDropMatrixFixture.
func insertGlobalPatternMatrixFixture(t *testing.T, db *bun.DB, r *rand.Rand, windows globalMatrixWindows) {
	t.Helper()
	rows := make([][]interface{}, 0)
	for _, server := range []string{"CN", "US"} {
		for _, sourceCategory := range allSourceCategories {
			for dayNum := globalMatrixFirstDay; dayNum <= globalMatrixLastDay; dayNum++ {
				for stageId := 1; stageId <= globalMatrixStageCount+1; stageId++ {
					windowStageId := min(stageId, globalMatrixStageCount)
					dayTimes := []int{0, 5, 12, 30}[r.Intn(4)]
					for calculation := r.Intn(3); calculation > 0; calculation-- {
						times := dayTimes
						if r.Intn(2) == 0 {
							times = []int{0, 5, 12, 30}[r.Intn(4)]
						}
						start, end := windows.elementWindow(r, windowStageId, dayNum)
						for patternId := 1; patternId <= 4; patternId++ {
							if r.Intn(3) == 0 {
								continue
							}
							quantity := r.Intn(5) - 1
							// stage 3 has times but never a quantity > 0 in the manual source category
							if quantity < 0 || times == 0 || stageId == 3 && sourceCategory == constant.SourceCategoryManual {
								quantity = 0
							}
							rows = append(rows, []interface{}{stageId, patternId, quantity, times, server, sourceCategory, start, end, dayNum})
						}
					}
				}
			}
		}
	}
	insertRows(t, db, "pattern_matrix_elements",
		"stage_id, pattern_id, quantity, times, server, source_category, start_time, end_time, day_num",
		"(?, ?, ?, ?, ?, ?, ?, ?, ?)", rows)
}

func (w globalMatrixWindows) globalPatternMatrixTargets(stageIds ...int) []*model.GlobalPatternMatrixTarget {
	targets := make([]*model.GlobalPatternMatrixTarget, 0, len(stageIds))
	for _, stageId := range stageIds {
		start, end := w[stageId][0], w[stageId][1]
		targets = append(targets, &model.GlobalPatternMatrixTarget{StageID: stageId, TimeRange: &model.TimeRange{StartTime: &start, EndTime: &end}})
	}
	return targets
}

// globalPatternMatrixAggregateStrings are the aggregates per source category in a canonical form: nil maps are
// empty, and a stage without quantities has none.
func globalPatternMatrixAggregateStrings(aggregates map[string]*model.GlobalPatternMatrixAggregates) map[string]string {
	strs := make(map[string]string, len(aggregates))
	for sourceCategory, a := range aggregates {
		times := a.TimesByStageID
		if times == nil {
			times = map[int]int{}
		}
		quantities := make(map[int]map[int]int)
		for stageId, q := range a.QuantitiesByStageID {
			if len(q) > 0 {
				quantities[stageId] = q
			}
		}
		// fmt prints maps sorted by key
		strs[sourceCategory] = fmt.Sprintf("times=%v quantities=%v", times, quantities)
	}
	return strs
}

func assertSameGlobalPatternMatrixAggregates(t *testing.T, what string, want, got map[string]*model.GlobalPatternMatrixAggregates) {
	t.Helper()
	w, g := globalPatternMatrixAggregateStrings(want), globalPatternMatrixAggregateStrings(got)
	if len(g) != len(w) {
		t.Fatalf("%s: aggregates of %d source categories, want %d", what, len(g), len(w))
	}
	for sourceCategory, ws := range w {
		if g[sourceCategory] != ws {
			t.Fatalf("%s: %s = %q, want %q", what, sourceCategory, g[sourceCategory], ws)
		}
	}
}

// mergeGlobalPatternMatrixAggregates adds up the aggregates of disjoint ranges of days per source category, into
// new aggregates.
func mergeGlobalPatternMatrixAggregates(parts ...map[string]*model.GlobalPatternMatrixAggregates) map[string]*model.GlobalPatternMatrixAggregates {
	merged := make(map[string]*model.GlobalPatternMatrixAggregates)
	for _, part := range parts {
		for sourceCategory, aggregates := range part {
			if _, ok := merged[sourceCategory]; !ok {
				merged[sourceCategory] = &model.GlobalPatternMatrixAggregates{}
			}
			merged[sourceCategory].Add(aggregates)
		}
	}
	return merged
}

func isEmptyGlobalPatternMatrixAggregates(aggregates map[string]*model.GlobalPatternMatrixAggregates) bool {
	for _, a := range aggregates {
		if len(a.TimesByStageID) > 0 || len(a.QuantitiesByStageID) > 0 {
			return false
		}
	}
	return true
}

// As TestCalcGlobalDropMatrixPartials, for the pattern matrices.
func TestCalcGlobalPatternMatrixPartials(t *testing.T) {
	db := newTestDB(t)
	repo := NewPatternMatrixElement(db)
	ctx := context.Background()
	r := rand.New(rand.NewSource(6))
	windows := newGlobalMatrixWindows()
	insertGlobalPatternMatrixFixture(t, db, r, windows)
	targets := windows.globalPatternMatrixTargets(allGlobalMatrixStageIds()...)

	for _, server := range []string{"CN", "US", "JP"} {
		for _, sourceCategories := range [][]string{{constant.SourceCategoryAll}, {constant.SourceCategoryManual}, allSourceCategories} {
			reference, err := repo.CalcGlobalPatternMatrixAggregates(ctx, db, server, targets, sourceCategories)
			if err != nil {
				t.Fatal(err)
			}
			if (server == "JP") != isEmptyGlobalPatternMatrixAggregates(reference) {
				t.Fatalf("%s %v: empty aggregates %v", server, sourceCategories, isEmptyGlobalPatternMatrixAggregates(reference))
			}
			if manual, ok := reference[constant.SourceCategoryManual]; ok && server != "JP" {
				if _, ok := manual.TimesByStageID[3]; !ok || len(manual.QuantitiesByStageID[3]) > 0 {
					t.Fatalf("%s: manual stage 3 has no times or has quantities", server)
				}
			}

			for _, scope := range globalMatrixScopes(r) {
				what := fmt.Sprintf("%s %v %+v", server, sourceCategories, scope)
				err := RunInSnapshotTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
					before, after, err := repo.CalcGlobalPatternMatrixPartials(ctx, tx, server, targets, sourceCategories, scope)
					if err != nil {
						return err
					}
					stageFirst := scope
					stageFirst.StageFirst = true
					stageFirstBefore, stageFirstAfter, err := repo.CalcGlobalPatternMatrixPartials(ctx, tx, server, targets, sourceCategories, stageFirst)
					if err != nil {
						return err
					}
					assertSameGlobalPatternMatrixAggregates(t, what+" before, stage first", before, stageFirstBefore)
					assertSameGlobalPatternMatrixAggregates(t, what+" after, stage first", after, stageFirstAfter)

					merged := mergeGlobalPatternMatrixAggregates(before, after)
					if scope.FromDayNum.Valid {
						fromDayNum := int(scope.FromDayNum.Int64)
						prior, _, err := repo.CalcGlobalPatternMatrixPartials(ctx, tx, server, targets, sourceCategories, model.GlobalMatrixScope{SplitDayNum: fromDayNum})
						if err != nil {
							return err
						}
						merged = mergeGlobalPatternMatrixAggregates(prior, merged)

						_, unbounded, err := repo.CalcGlobalPatternMatrixPartials(ctx, tx, server, targets, sourceCategories, model.GlobalMatrixScope{SplitDayNum: scope.SplitDayNum})
						if err != nil {
							return err
						}
						if fromDayNum <= scope.SplitDayNum {
							assertSameGlobalPatternMatrixAggregates(t, what+" after, against no from day", unbounded, after)
						} else if !isEmptyGlobalPatternMatrixAggregates(before) {
							t.Fatalf("%s: aggregates before a split before the from day", what)
						}
					}
					assertSameGlobalPatternMatrixAggregates(t, what+" merged, against the reference", reference, merged)
					if scope.SplitDayNum <= globalMatrixFirstDay && !isEmptyGlobalPatternMatrixAggregates(before) {
						t.Fatalf("%s: aggregates before the first day", what)
					}
					if scope.SplitDayNum > globalMatrixLastDay && !isEmptyGlobalPatternMatrixAggregates(after) {
						t.Fatalf("%s: aggregates after the last day", what)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}

			stageTargets := windows.globalPatternMatrixTargets(2, globalMatrixStageCount)
			stageReference, err := repo.CalcGlobalPatternMatrixAggregates(ctx, db, server, stageTargets, sourceCategories)
			if err != nil {
				t.Fatal(err)
			}
			for sourceCategory, aggregates := range reference {
				for stageId := range aggregates.TimesByStageID {
					if stageId != 2 && stageId != globalMatrixStageCount {
						continue
					}
					if aggregates.TimesByStageID[stageId] != stageReference[sourceCategory].TimesByStageID[stageId] ||
						fmt.Sprint(aggregates.QuantitiesByStageID[stageId]) != fmt.Sprint(stageReference[sourceCategory].QuantitiesByStageID[stageId]) {
						t.Fatalf("%s %s: stage %d differs with the targets of a few stages", server, sourceCategory, stageId)
					}
				}
			}
			split := globalMatrixFirstDay + r.Intn(globalMatrixLastDay-globalMatrixFirstDay+1)
			before, after, err := repo.CalcGlobalPatternMatrixPartials(ctx, db, server, stageTargets, sourceCategories,
				model.GlobalMatrixScope{SplitDayNum: split, StageFirst: true})
			if err != nil {
				t.Fatal(err)
			}
			assertSameGlobalPatternMatrixAggregates(t, fmt.Sprintf("%s %v stages 2 and %d split at %d", server, sourceCategories, globalMatrixStageCount, split),
				stageReference, mergeGlobalPatternMatrixAggregates(before, after))
		}
	}
}
