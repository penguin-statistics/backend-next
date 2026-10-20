package repo

import (
	"context"
	"math/rand"
	"testing"

	"exusiai.dev/gommon/constant"
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
