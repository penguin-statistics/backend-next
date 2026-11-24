package repo

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
)

type siteStatsElement struct {
	stageId, itemId, quantity, times, dayNum int
	server, sourceCategory                   string
}

// referenceSiteStatsAggregates is the documented meaning of model.SiteStatsAggregate, computed in Go: per stage and
// item, times summed over the distinct (times, day_num) pairs with times > 0, and quantity summed over all elements.
func referenceSiteStatsAggregates(elements []siteStatsElement, server string, fromDayNum, toDayNum null.Int) map[[2]int]model.SiteStatsAggregate {
	type timesKey struct{ stageId, itemId, times, dayNum int }
	seen := make(map[timesKey]bool)
	aggregates := make(map[[2]int]model.SiteStatsAggregate)
	for _, el := range elements {
		if el.server != server || el.sourceCategory != constant.SourceCategoryAll ||
			(fromDayNum.Valid && el.dayNum < int(fromDayNum.Int64)) || (toDayNum.Valid && el.dayNum >= int(toDayNum.Int64)) {
			continue
		}
		k := [2]int{el.stageId, el.itemId}
		aggregate := aggregates[k]
		aggregate.StageID, aggregate.ItemID = el.stageId, el.itemId
		aggregate.Quantity += el.quantity
		tk := timesKey{el.stageId, el.itemId, el.times, el.dayNum}
		if el.times > 0 && !seen[tk] {
			seen[tk] = true
			aggregate.Times = null.IntFrom(aggregate.Times.Int64 + int64(el.times))
		}
		aggregates[k] = aggregate
	}
	return aggregates
}

func siteStatsAggregatesByKey(t *testing.T, aggregates []*model.SiteStatsAggregate) map[[2]int]model.SiteStatsAggregate {
	t.Helper()
	m := make(map[[2]int]model.SiteStatsAggregate)
	for _, aggregate := range aggregates {
		k := [2]int{aggregate.StageID, aggregate.ItemID}
		if _, ok := m[k]; ok {
			t.Fatalf("duplicate aggregate of stage %d item %d", k[0], k[1])
		}
		m[k] = *aggregate
	}
	return m
}

func assertSameSiteStatsAggregates(t *testing.T, what string, want, got map[[2]int]model.SiteStatsAggregate) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d aggregates, want %d", what, len(got), len(want))
	}
	for k, w := range want {
		if got[k] != w {
			t.Fatalf("%s: stage %d item %d = %+v, want %+v", what, k[0], k[1], got[k], w)
		}
	}
}

func TestCalcSiteStatsAggregates(t *testing.T) {
	db := newTestDB(t)
	repo := NewDropMatrixElement(db)
	ctx := context.Background()
	r := rand.New(rand.NewSource(3))

	// elements of several days, servers and source categories; the elements of a stage on a day share times (they
	// come from the same calculation) unless they belong to different time ranges, which may repeat times
	elements := make([]siteStatsElement, 0)
	for dayNum := 100; dayNum < 110; dayNum++ {
		for _, server := range []string{"CN", "US"} {
			for stageId := 1; stageId <= 4; stageId++ {
				for timeRange := 0; timeRange < 1+r.Intn(2); timeRange++ {
					times := r.Intn(4) * 10 // times = 0 elements exist too
					for itemId := 1; itemId <= 1+r.Intn(4); itemId++ {
						for _, sourceCategory := range allSourceCategories {
							elements = append(elements, siteStatsElement{
								stageId: stageId, itemId: itemId, quantity: r.Intn(30), times: times,
								dayNum: dayNum, server: server, sourceCategory: sourceCategory,
							})
						}
					}
				}
			}
		}
	}
	for _, el := range elements {
		mustExec(t, db, `INSERT INTO drop_matrix_elements (stage_id, item_id, quantity, times, server, source_category, day_num)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, el.stageId, el.itemId, el.quantity, el.times, el.server, el.sourceCategory, el.dayNum)
	}

	for _, server := range []string{"CN", "US", "JP"} {
		for _, bounds := range [][2]null.Int{
			{{}, {}},
			{null.IntFrom(103), {}},
			{{}, null.IntFrom(107)},
			{null.IntFrom(103), null.IntFrom(107)},
			{null.IntFrom(105), null.IntFrom(105)},
		} {
			got, err := repo.CalcSiteStatsAggregates(ctx, server, bounds[0], bounds[1])
			if err != nil {
				t.Fatal(err)
			}
			assertSameSiteStatsAggregates(t, server+" against the reference",
				referenceSiteStatsAggregates(elements, server, bounds[0], bounds[1]), siteStatsAggregatesByKey(t, got))
		}

		// what the site stats do: a cached base of the days before a split, plus the days from the split on
		all, err := repo.CalcSiteStatsAggregates(ctx, server, null.Int{}, null.Int{})
		if err != nil {
			t.Fatal(err)
		}
		for split := int64(99); split <= 110; split++ {
			base, err := repo.CalcSiteStatsAggregates(ctx, server, null.Int{}, null.IntFrom(split))
			if err != nil {
				t.Fatal(err)
			}
			recent, err := repo.CalcSiteStatsAggregates(ctx, server, null.IntFrom(split), null.Int{})
			if err != nil {
				t.Fatal(err)
			}
			merged := siteStatsAggregatesByKey(t, base)
			for _, aggregate := range recent {
				k := [2]int{aggregate.StageID, aggregate.ItemID}
				m, ok := merged[k]
				if !ok {
					merged[k] = *aggregate
					continue
				}
				m.Add(aggregate)
				merged[k] = m
			}
			assertSameSiteStatsAggregates(t, server+" base and recent days merged", siteStatsAggregatesByKey(t, all), merged)
		}
	}
}

// globalMatrixWindows are the target windows of the global matrix partials tests, per stage: with millisecond
// fractions, which the aggregations truncate to seconds; the last stage never ends (constant.FakeEndTimeMilli).
type globalMatrixWindows map[int][2]time.Time

const (
	globalMatrixFirstDay   = 100
	globalMatrixLastDay    = 117
	globalMatrixStageCount = 6
)

func newGlobalMatrixWindows() globalMatrixWindows {
	windows := make(globalMatrixWindows)
	for stageId := 1; stageId <= globalMatrixStageCount; stageId++ {
		start := time.Date(2026, 1, 1, stageId, 0, 0, 250*int(time.Millisecond), time.UTC)
		end := start.Add(40*24*time.Hour + 500*time.Millisecond)
		if stageId == globalMatrixStageCount {
			end = time.UnixMilli(constant.FakeEndTimeMilli)
		}
		windows[stageId] = [2]time.Time{start, end}
	}
	return windows
}

// elementWindow returns a window of an element of stage on dayNum: the target window at second precision, a window
// within it, one starting a second before or ending a second after it, one never ending (within the target window
// of the last stage only), or one starting at the target start with its milliseconds.
func (w globalMatrixWindows) elementWindow(r *rand.Rand, stageId, dayNum int) (time.Time, time.Time) {
	start, end := w[stageId][0].Truncate(time.Second), w[stageId][1].Truncate(time.Second)
	dayStart := start.Add(time.Duration(dayNum-globalMatrixFirstDay) * 24 * time.Hour)
	switch r.Intn(8) {
	case 0:
		return start.Add(-time.Second), dayStart.Add(24 * time.Hour)
	case 1:
		return dayStart, end.Add(time.Second)
	case 2:
		return dayStart, time.UnixMilli(constant.FakeEndTimeMilli)
	case 3:
		return w[stageId][0], dayStart.Add(24 * time.Hour)
	case 4:
		return start, end
	default:
		return dayStart, dayStart.Add(24 * time.Hour)
	}
}

// insertRows inserts rows of values for columns into table, many at a time.
func insertRows(t *testing.T, db *bun.DB, table string, columns string, placeholders string, rows [][]interface{}) {
	t.Helper()
	for len(rows) > 0 {
		n := min(len(rows), 500)
		values := make([]string, n)
		args := make([]interface{}, 0, n*len(rows[0]))
		for i, row := range rows[:n] {
			values[i] = placeholders
			args = append(args, row...)
		}
		mustExec(t, db, "INSERT INTO "+table+" ("+columns+") VALUES "+strings.Join(values, ", "), args...)
		rows = rows[n:]
	}
}

// insertGlobalDropMatrixFixture inserts random drop matrix elements of stages 1 to globalMatrixStageCount and items 1
// to 5 on CN and US for every source category, with the edge cases of the global aggregation: elements with quantity
// 0 (buckets {0: times}), two elements of a day with the same and with different times, elements just outside the
// target windows or never ending, and elements with quantity > 0 with {} or NULL buckets; a target whose elements
// all have quantity 0, and one whose elements all have no buckets.
func insertGlobalDropMatrixFixture(t *testing.T, db *bun.DB, r *rand.Rand, windows globalMatrixWindows) {
	t.Helper()
	rows := make([][]interface{}, 0)
	for _, server := range []string{"CN", "US"} {
		for _, sourceCategory := range allSourceCategories {
			for dayNum := globalMatrixFirstDay; dayNum <= globalMatrixLastDay; dayNum++ {
				for stageId := 1; stageId <= globalMatrixStageCount; stageId++ {
					dayTimes := []int{0, 10, 20, 30}[r.Intn(4)]
					for itemId := 1; itemId <= 5; itemId++ {
						for n := r.Intn(3); n > 0; n-- {
							times := dayTimes
							if r.Intn(3) == 0 {
								times = []int{0, 10, 20, 30}[r.Intn(4)]
							}
							quantity := r.Intn(6) - 2
							// item 3 of stage 3 never drops, that of stage 4 has no buckets
							if quantity < 0 || stageId == 3 && itemId == 3 {
								quantity = 0
							}
							var buckets interface{}
							switch {
							case quantity == 0 && r.Intn(4) > 0:
								buckets = fmt.Sprintf(`{"0": %d}`, times)
							case quantity == 0:
							case r.Intn(5) == 0 || stageId == 4 && itemId == 3 && r.Intn(2) == 0:
								buckets = `{}`
							case r.Intn(5) == 0 || stageId == 4 && itemId == 3:
							case quantity > 1 && r.Intn(2) == 0:
								buckets = fmt.Sprintf(`{"1": %d, "%d": 1}`, quantity-1, quantity)
							default:
								buckets = fmt.Sprintf(`{"%d": 1}`, quantity)
							}
							start, end := windows.elementWindow(r, stageId, dayNum)
							rows = append(rows, []interface{}{stageId, itemId, quantity, times, server, buckets, sourceCategory, start, end, dayNum})
						}
					}
				}
			}
		}
	}
	insertRows(t, db, "drop_matrix_elements",
		"stage_id, item_id, quantity, times, server, quantity_buckets, source_category, start_time, end_time, day_num",
		"(?, ?, ?, ?, ?, ?::jsonb, ?, ?, ?, ?)", rows)
}

// globalDropMatrixTargets returns the targets of items 1 to 4 of stages, item 5 is none; item 4 of stage 2 has a
// window starting 5 days later than the others of its stage.
func (w globalMatrixWindows) globalDropMatrixTargets(stageIds ...int) []*model.GlobalDropMatrixTarget {
	targets := make([]*model.GlobalDropMatrixTarget, 0)
	for _, stageId := range stageIds {
		for itemId := 1; itemId <= 4; itemId++ {
			start, end := w[stageId][0], w[stageId][1]
			if stageId == 2 && itemId == 4 {
				start = start.Add(5 * 24 * time.Hour)
			}
			targets = append(targets, &model.GlobalDropMatrixTarget{StageID: stageId, ItemID: itemId, TimeRange: &model.TimeRange{StartTime: &start, EndTime: &end}})
		}
	}
	return targets
}

func allGlobalMatrixStageIds() []int {
	stageIds := make([]int, globalMatrixStageCount)
	for i := range stageIds {
		stageIds[i] = i + 1
	}
	return stageIds
}

// globalDropMatrixAggregateStrings are aggregates in a canonical form, without those with nothing valid (absent).
func globalDropMatrixAggregateStrings(aggregates map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate) map[model.GlobalDropMatrixKey]string {
	strs := make(map[model.GlobalDropMatrixKey]string, len(aggregates))
	for key, a := range aggregates {
		if !a.Times.Valid && !a.Quantity.Valid && len(a.QuantityBuckets) == 0 {
			continue
		}
		buckets := a.QuantityBuckets
		if buckets == nil {
			buckets = map[int]int{}
		}
		// fmt prints maps sorted by key
		strs[key] = fmt.Sprintf("times=%v quantity=%v buckets=%v", a.Times, a.Quantity, buckets)
	}
	return strs
}

func assertSameGlobalDropMatrixAggregates(t *testing.T, what string, want, got map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate) {
	t.Helper()
	w, g := globalDropMatrixAggregateStrings(want), globalDropMatrixAggregateStrings(got)
	if len(g) != len(w) {
		t.Fatalf("%s: %d aggregates, want %d", what, len(g), len(w))
	}
	for key, ws := range w {
		if g[key] != ws {
			t.Fatalf("%s: %+v = %q, want %q", what, key, g[key], ws)
		}
	}
}

// mergeGlobalDropMatrixAggregates adds up the aggregates of disjoint ranges of days per key, into new aggregates.
func mergeGlobalDropMatrixAggregates(parts ...map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate) map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate {
	merged := make(map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate)
	for _, part := range parts {
		for key, aggregate := range part {
			if _, ok := merged[key]; !ok {
				merged[key] = &model.GlobalDropMatrixAggregate{}
			}
			merged[key].Add(aggregate)
		}
	}
	return merged
}

// globalMatrixScopes returns scopes splitting the fixture's days at random and at the edges, from no day, a random
// day or a day after the split.
func globalMatrixScopes(r *rand.Rand) []model.GlobalMatrixScope {
	splits := []int{globalMatrixFirstDay - 5, globalMatrixFirstDay, globalMatrixFirstDay + 1, globalMatrixLastDay, globalMatrixLastDay + 1, globalMatrixLastDay + 5}
	for i := 0; i < 6; i++ {
		splits = append(splits, globalMatrixFirstDay+r.Intn(globalMatrixLastDay-globalMatrixFirstDay+1))
	}
	scopes := make([]model.GlobalMatrixScope, 0)
	for _, split := range splits {
		lowest := min(split, globalMatrixFirstDay) - 1
		scopes = append(scopes,
			model.GlobalMatrixScope{SplitDayNum: split},
			model.GlobalMatrixScope{SplitDayNum: split, FromDayNum: null.IntFrom(int64(lowest + r.Intn(split-lowest+1)))},
			model.GlobalMatrixScope{SplitDayNum: split, FromDayNum: null.IntFrom(int64(split + r.Intn(3)))},
		)
	}
	return scopes
}

// For any split and from day, the aggregates of the days before the split plus those of the days after are those of
// CalcGlobalDropMatrixAggregates, the reference; with a from day, plus those of the days before it.
func TestCalcGlobalDropMatrixPartials(t *testing.T) {
	db := newTestDB(t)
	repo := NewDropMatrixElement(db)
	ctx := context.Background()
	r := rand.New(rand.NewSource(5))
	windows := newGlobalMatrixWindows()
	insertGlobalDropMatrixFixture(t, db, r, windows)
	targets := windows.globalDropMatrixTargets(allGlobalMatrixStageIds()...)

	for _, server := range []string{"CN", "US", "JP"} {
		for _, sourceCategories := range [][]string{{constant.SourceCategoryAll}, {constant.SourceCategoryManual}, allSourceCategories} {
			reference, err := repo.CalcGlobalDropMatrixAggregates(ctx, db, server, targets, sourceCategories)
			if err != nil {
				t.Fatal(err)
			}
			if server != "JP" {
				// the edge cases are there: times without quantity, quantity without buckets
				var timesOnly, noBuckets int
				for _, a := range reference {
					if a.Times.Valid && !a.Quantity.Valid {
						timesOnly++
					}
					if a.Quantity.Valid && len(a.QuantityBuckets) == 0 {
						noBuckets++
					}
				}
				if len(reference) < 20 || timesOnly == 0 || noBuckets == 0 {
					t.Fatalf("%s %v: a fixture without its edge cases: %d aggregates, %d with times only, %d without buckets",
						server, sourceCategories, len(reference), timesOnly, noBuckets)
				}
			} else if len(reference) != 0 {
				t.Fatalf("JP has %d aggregates, want none", len(reference))
			}

			for _, scope := range globalMatrixScopes(r) {
				what := fmt.Sprintf("%s %v %+v", server, sourceCategories, scope)
				// the way the refresh reads them: in a snapshot transaction
				err := RunInSnapshotTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
					before, after, err := repo.CalcGlobalDropMatrixPartials(ctx, tx, server, targets, sourceCategories, scope)
					if err != nil {
						return err
					}
					stageFirst := scope
					stageFirst.StageFirst = true
					stageFirstBefore, stageFirstAfter, err := repo.CalcGlobalDropMatrixPartials(ctx, tx, server, targets, sourceCategories, stageFirst)
					if err != nil {
						return err
					}
					assertSameGlobalDropMatrixAggregates(t, what+" before, stage first", before, stageFirstBefore)
					assertSameGlobalDropMatrixAggregates(t, what+" after, stage first", after, stageFirstAfter)

					merged := mergeGlobalDropMatrixAggregates(before, after)
					if scope.FromDayNum.Valid {
						fromDayNum := int(scope.FromDayNum.Int64)
						prior, _, err := repo.CalcGlobalDropMatrixPartials(ctx, tx, server, targets, sourceCategories, model.GlobalMatrixScope{SplitDayNum: fromDayNum})
						if err != nil {
							return err
						}
						merged = mergeGlobalDropMatrixAggregates(prior, merged)

						// the from day leaves out days before the split only
						_, unbounded, err := repo.CalcGlobalDropMatrixPartials(ctx, tx, server, targets, sourceCategories, model.GlobalMatrixScope{SplitDayNum: scope.SplitDayNum})
						if err != nil {
							return err
						}
						if fromDayNum <= scope.SplitDayNum {
							assertSameGlobalDropMatrixAggregates(t, what+" after, against no from day", unbounded, after)
						} else if len(before) != 0 {
							t.Fatalf("%s: %d aggregates before a split before the from day", what, len(before))
						}
					}
					assertSameGlobalDropMatrixAggregates(t, what+" merged, against the reference", reference, merged)
					if scope.SplitDayNum <= globalMatrixFirstDay && len(before) != 0 {
						t.Fatalf("%s: %d aggregates before the first day", what, len(before))
					}
					if scope.SplitDayNum > globalMatrixLastDay && len(after) != 0 {
						t.Fatalf("%s: %d aggregates after the last day", what, len(after))
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}

			// the targets of a few stages only, read stage first, as for targets whose window changed
			stageTargets := windows.globalDropMatrixTargets(2, globalMatrixStageCount)
			stageReference, err := repo.CalcGlobalDropMatrixAggregates(ctx, db, server, stageTargets, sourceCategories)
			if err != nil {
				t.Fatal(err)
			}
			ofStages := make(map[model.GlobalDropMatrixKey]*model.GlobalDropMatrixAggregate)
			for key, aggregate := range reference {
				if key.StageID == 2 || key.StageID == globalMatrixStageCount {
					ofStages[key] = aggregate
				}
			}
			assertSameGlobalDropMatrixAggregates(t, fmt.Sprintf("%s %v the targets of stages 2 and %d", server, sourceCategories, globalMatrixStageCount),
				ofStages, stageReference)
			split := globalMatrixFirstDay + r.Intn(globalMatrixLastDay-globalMatrixFirstDay+1)
			before, after, err := repo.CalcGlobalDropMatrixPartials(ctx, db, server, stageTargets, sourceCategories,
				model.GlobalMatrixScope{SplitDayNum: split, StageFirst: true})
			if err != nil {
				t.Fatal(err)
			}
			assertSameGlobalDropMatrixAggregates(t, fmt.Sprintf("%s %v stages 2 and %d split at %d", server, sourceCategories, globalMatrixStageCount, split),
				stageReference, mergeGlobalDropMatrixAggregates(before, after))
		}
	}
}
