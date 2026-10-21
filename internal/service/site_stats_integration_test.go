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
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	pkgcache "exusiai.dev/backend-next/internal/pkg/cache"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/util"
)

// The site stats queries of v3.14.18, which aggregated every drop matrix element of a server on each refresh.

func legacyCalcTotalItemQuantityForShimSiteStats(ctx context.Context, db *bun.DB, server string) ([]*modelv2.TotalItemQuantity, error) {
	types := []string{constant.ItemTypeMaterial, constant.ItemTypeFurniture, constant.ItemTypeChip}

	subq := db.NewSelect().
		TableExpr("drop_matrix_elements").
		Column("item_id").
		ColumnExpr("SUM(quantity) AS total_quantity").
		Where("server = ?", server).
		Where("source_category = ?", constant.SourceCategoryAll).
		Group("item_id")

	mainq := db.NewSelect().
		Table("items").
		TableExpr("(?) AS subq", subq).
		Column("ark_item_id", "total_quantity").
		Where("subq.item_id = items.item_id").
		Where("items.type IN (?)", bun.In(types))

	results := make([]*modelv2.TotalItemQuantity, 0)
	err := mainq.Scan(ctx, &results)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func legacyCalcTotalStageQuantityForShimSiteStats(ctx context.Context, db *bun.DB, server string) ([]*modelv2.TotalStageTime, error) {
	results := make([]*modelv2.TotalStageTime, 0)
	err := legacyGetStageTimesQuery(db, server, false).Scan(ctx, &results)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func legacyCalcTotalSanityCostForShimSiteStats(ctx context.Context, db *bun.DB, server string) (sanity int, err error) {
	mainq := db.NewSelect().
		TableExpr("(?) AS subq4", legacyGetStageTimesQuery(db, server, true)).
		ColumnExpr("SUM(sanity * total_times) AS sanity")
	mainq.Scan(ctx, &sanity)
	return sanity, err
}

func legacyGetStageTimesQuery(db *bun.DB, server string, containsSanity bool) *bun.SelectQuery {
	subq3 := db.NewSelect().
		TableExpr("drop_matrix_elements").
		Column("stage_id", "item_id", "times", "day_num").
		Where("server = ?", server).
		Where("source_category = ?", constant.SourceCategoryAll).
		Where("times > 0")

	subq2 := db.NewSelect().
		TableExpr("(?) AS subq3", subq3).
		Column("stage_id", "item_id", "times").
		Group("stage_id", "item_id", "times", "day_num")

	subq1 := db.NewSelect().
		TableExpr("(?) AS subq2", subq2).
		Column("stage_id").
		ColumnExpr("sum(times) AS total_times").
		Group("stage_id", "item_id")

	mainq := db.NewSelect().
		Table("stages").
		TableExpr("(?) AS subq1", subq1)

	if containsSanity {
		mainq = mainq.Column("ark_stage_id", "sanity")
	} else {
		mainq = mainq.Column("ark_stage_id")
	}

	mainq.ColumnExpr("max(total_times) AS total_times").
		Where("subq1.stage_id = stages.stage_id").
		Where("ark_stage_id != ?", "recruit").
		Where("extra_process_type IS NULL")

	if containsSanity {
		mainq = mainq.Group("ark_stage_id", "sanity")
	} else {
		mainq = mainq.Group("ark_stage_id")
	}
	return mainq
}

// seedSiteStatsData inserts stages (a recruit stage, a gacha box stage, one without sanity), items of the counted
// and other types, and drop matrix elements of every server and source category on the days at dayOffsets from today.
func seedSiteStatsData(t *testing.T, r *rand.Rand, db *bun.DB, today map[string]int, dayOffsets []int) {
	t.Helper()
	for stageId := 1; stageId <= 8; stageId++ {
		arkStageId := fmt.Sprintf("main_%02d", stageId)
		var extraProcessType, sanity interface{} = nil, 6 + stageId
		switch stageId {
		case 1:
			arkStageId = constant.RecruitStageID
		case 2:
			extraProcessType = "GACHABOX"
		case 3:
			sanity = nil
		}
		mustExec(t, db, "INSERT INTO stages (stage_id, ark_stage_id, zone_id, stage_type, extra_process_type, sanity) VALUES (?, ?, 1, 'MAIN', ?, ?)",
			stageId, arkStageId, extraProcessType, sanity)
	}
	itemTypes := []string{constant.ItemTypeMaterial, constant.ItemTypeFurniture, constant.ItemTypeChip, "CARD_EXP", "ACTIVITY_ITEM"}
	for itemId := 10; itemId <= 19; itemId++ {
		mustExec(t, db, "INSERT INTO items (item_id, ark_item_id, type) VALUES (?, ?, ?)", itemId, fmt.Sprintf("ark_%d", itemId), itemTypes[itemId%len(itemTypes)])
	}
	for _, dayOffset := range dayOffsets {
		insertSiteStatsDay(t, r, db, today, dayOffset)
	}
}

// insertSiteStatsDay inserts the drop matrix elements of the day at dayOffset from today (day numbers differ between
// servers): per stage and time range one times value shared by the stage's items, as the worker writes them,
// including times = 0 elements.
func insertSiteStatsDay(t *testing.T, r *rand.Rand, db *bun.DB, today map[string]int, dayOffset int) {
	t.Helper()
	for _, server := range testServers {
		dayNum := today[server] + dayOffset
		for stageId := 1; stageId <= 8; stageId++ {
			if r.Intn(5) == 0 {
				continue
			}
			for timeRange := 0; timeRange < 1+r.Intn(2); timeRange++ {
				times := r.Intn(5) * 7
				for itemId := 10; itemId <= 19; itemId++ {
					if r.Intn(2) == 0 {
						continue
					}
					for _, sourceCategory := range []string{constant.SourceCategoryAll, constant.SourceCategoryManual, constant.SourceCategoryAutomated} {
						mustExec(t, db, `INSERT INTO drop_matrix_elements (stage_id, item_id, quantity, times, server, source_category, day_num)
							VALUES (?, ?, ?, ?, ?, ?, ?)`, stageId, itemId, r.Intn(40), times, server, sourceCategory, dayNum)
					}
				}
			}
		}
	}
}

func siteStatsLines(stats *modelv2.SiteStats) []string {
	lines := []string{fmt.Sprintf("sanity|%d", stats.TotalSanityCost)}
	for _, stageTime := range stats.TotalStageTimes {
		lines = append(lines, fmt.Sprintf("stage|%s|%d", stageTime.StageID, stageTime.Times))
	}
	for _, itemQuantity := range stats.TotalItemQuantities {
		lines = append(lines, fmt.Sprintf("item|%s|%d", itemQuantity.ItemID, itemQuantity.Quantity))
	}
	sort.Strings(lines)
	return lines
}

func legacySiteStatsLines(t *testing.T, db *bun.DB, server string) []string {
	t.Helper()
	ctx := context.Background()
	stageTimes, err := legacyCalcTotalStageQuantityForShimSiteStats(ctx, db, server)
	if err != nil {
		t.Fatal(err)
	}
	itemQuantities, err := legacyCalcTotalItemQuantityForShimSiteStats(ctx, db, server)
	if err != nil {
		t.Fatal(err)
	}
	sanity, err := legacyCalcTotalSanityCostForShimSiteStats(ctx, db, server)
	if err != nil {
		t.Fatal(err)
	}
	return siteStatsLines(&modelv2.SiteStats{TotalStageTimes: stageTimes, TotalItemQuantities: itemQuantities, TotalSanityCost: sanity})
}

func TestRefreshShimSiteStatsMatchesLegacy(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	r := rand.New(rand.NewSource(1))
	flushCaches(t)

	now := time.Date(2026, 10, 19, 14, 0, 0, 0, time.UTC)
	util.Now = func() time.Time { return now }
	t.Cleanup(func() { util.Now = time.Now })
	today := make(map[string]int)
	for _, server := range testServers {
		today[server] = util.GetDayNum(&now, server)
	}
	seedSiteStatsData(t, r, db, today, []int{-6, -3, -2, -1, 0})

	generations := &memorySiteStatsBaseGenerations{}
	s := &SiteStats{
		DropReportService:        NewDropReport(repo.NewDropReport(db)),
		DropMatrixElementService: NewDropMatrixElement(repo.NewDropMatrixElement(db)),
		StageService:             NewStage(repo.NewStage(db)),
		ItemService:              NewItem(repo.NewItem(db)),
		baseGenerations:          generations,
	}
	compared := 0
	check := func(what string) {
		t.Helper()
		for _, server := range testServers {
			stats, err := s.RefreshShimSiteStats(ctx, server)
			if err != nil {
				t.Fatal(err)
			}
			compared += assertSameLines(t, what+" "+server, legacySiteStatsLines(t, db, server), siteStatsLines(stats))
			cached, err := s.GetShimSiteStats(ctx, server)
			if err != nil {
				t.Fatal(err)
			}
			compared += assertSameLines(t, what+" "+server+" cached", siteStatsLines(stats), siteStatsLines(cached))
		}
	}

	check("first refresh")
	// a batch rewrites today and yesterday; the base of the days before is reused
	for _, server := range testServers {
		mustExec(t, db, "DELETE FROM drop_matrix_elements WHERE server = ? AND day_num >= ?", server, today[server]-1)
	}
	insertSiteStatsDay(t, r, db, today, -1)
	insertSiteStatsDay(t, r, db, today, 0)
	check("after a batch")

	// an admin recalculation of a past day invalidates the base
	for _, server := range testServers {
		mustExec(t, db, "DELETE FROM drop_matrix_elements WHERE server = ? AND day_num = ?", server, today[server]-3)
	}
	insertSiteStatsDay(t, r, db, today, -3)
	for _, server := range testServers {
		if err := s.InvalidateSiteStatsBase(ctx, server); err != nil {
			t.Fatal(err)
		}
	}
	check("after a recalculation of a past day")

	// the next day: the day before yesterday moves into the base
	now = now.Add(24 * time.Hour)
	insertSiteStatsDay(t, r, db, today, 1)
	check("on the next day")

	requireCompared(t, compared, 100)
}

// countingSiteStatsBaseGenerations counts the site stats calculations: each reads the generation once.
type countingSiteStatsBaseGenerations struct {
	memorySiteStatsBaseGenerations
	gets atomic.Int32
}

func (g *countingSiteStatsBaseGenerations) Get(ctx context.Context, server string) (int64, error) {
	g.gets.Add(1)
	return g.memorySiteStatsBaseGenerations.Get(ctx, server)
}

func TestShimSiteStatsCache(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	flushCaches(t)
	now := time.Date(2026, 10, 19, 14, 0, 0, 0, time.UTC)
	util.Now = func() time.Time { return now }
	t.Cleanup(func() { util.Now = time.Now })
	today := make(map[string]int)
	for _, server := range testServers {
		today[server] = util.GetDayNum(&now, server)
	}
	seedSiteStatsData(t, rand.New(rand.NewSource(1)), db, today, []int{-3, -1, 0})
	calculations := &countingSiteStatsBaseGenerations{}
	s := &SiteStats{
		DropReportService:        NewDropReport(repo.NewDropReport(db)),
		DropMatrixElementService: NewDropMatrixElement(repo.NewDropMatrixElement(db)),
		StageService:             NewStage(repo.NewStage(db)),
		ItemService:              NewItem(repo.NewItem(db)),
		baseGenerations:          calculations,
	}
	want := legacySiteStatsLines(t, db, "CN")

	// concurrent requests on an empty cache calculate the stats once
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stats, err := s.GetShimSiteStats(ctx, "CN")
			if err != nil {
				t.Error(err)
				return
			}
			assertSameLines(t, "request", want, siteStatsLines(stats))
		}()
	}
	wg.Wait()
	if n := calculations.gets.Load(); n != 1 {
		t.Errorf("concurrent requests calculated the stats %d times, want once", n)
	}
	var lastModified time.Time
	if err := cache.LastModifiedTime.Get("[shimSiteStats#server:CN]", &lastModified); err != nil {
		t.Error("no last modified time for the stats a request calculated")
	}

	// requests get the cached stats until a refresh replaces them, also while it calculates, without waiting for it
	stale := modelv2.SiteStats{TotalSanityCost: -1}
	cache.ShimSiteStats.Set("CN", stale, time.Hour)
	if stats, err := s.GetShimSiteStats(ctx, "CN"); err != nil || stats.TotalSanityCost != -1 {
		t.Fatalf("a request got %+v, %v instead of the cached stats", stats, err)
	}
	var refreshed *modelv2.SiteStats
	requestWhileRefreshWaits(t, db, "drop_matrix_elements", func() (err error) {
		refreshed, err = s.RefreshShimSiteStats(ctx, "CN")
		return err
	}, func() {
		if stats, err := s.GetShimSiteStats(ctx, "CN"); err != nil || stats.TotalSanityCost != -1 {
			t.Errorf("a request during a refresh got %+v, %v instead of the cached stats", stats, err)
		}
	})
	assertSameLines(t, "refresh", want, siteStatsLines(refreshed))
	cached, err := s.GetShimSiteStats(ctx, "CN")
	if err != nil {
		t.Fatal(err)
	}
	assertSameLines(t, "refreshed cache", want, siteStatsLines(cached))
	if n := calculations.gets.Load(); n != 2 {
		t.Errorf("calculated the stats %d times, want twice", n)
	}

	// a failed refresh drops the cached stats, so that requests recalculate them, and only those of its server
	cache.ShimSiteStats.Set("US", stale, time.Hour)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.RefreshShimSiteStats(cancelled, "CN"); err == nil {
		t.Fatal("refreshing with a cancelled context succeeded")
	}
	var stats modelv2.SiteStats
	if err := cache.ShimSiteStats.Get("CN", &stats); !errors.Is(err, pkgcache.ErrNotFound) {
		t.Errorf("a failed refresh kept the cached stats: %v", err)
	}
	if err := cache.ShimSiteStats.Get("US", &stats); err != nil || stats.TotalSanityCost != -1 {
		t.Errorf("a failed refresh of CN dropped the stats of another server: %v", err)
	}
}

type pauseQueriesKey struct{}

// pauseQueriesHook holds the queries of a context marked with pauseQueriesKey that read table, after they ran, until
// release is closed; paused is closed when the first one is held.
type pauseQueriesHook struct {
	table   string
	once    sync.Once
	paused  chan struct{}
	release chan struct{}
}

func (h *pauseQueriesHook) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

func (h *pauseQueriesHook) AfterQuery(ctx context.Context, event *bun.QueryEvent) {
	if ctx.Value(pauseQueriesKey{}) == nil || !strings.Contains(event.Query, h.table) {
		return
	}
	h.once.Do(func() { close(h.paused) })
	<-h.release
}

func TestShimSiteStatsOverlappingRefreshes(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	flushCaches(t)
	now := time.Date(2026, 10, 19, 14, 0, 0, 0, time.UTC)
	util.Now = func() time.Time { return now }
	t.Cleanup(func() { util.Now = time.Now })
	today := make(map[string]int)
	for _, server := range testServers {
		today[server] = util.GetDayNum(&now, server)
	}
	seedSiteStatsData(t, rand.New(rand.NewSource(1)), db, today, []int{-3, -1, 0})
	hook := &pauseQueriesHook{table: "drop_matrix_elements", paused: make(chan struct{}), release: make(chan struct{})}
	db.AddQueryHook(hook)
	s := &SiteStats{
		DropReportService:        NewDropReport(repo.NewDropReport(db)),
		DropMatrixElementService: NewDropMatrixElement(repo.NewDropMatrixElement(db)),
		StageService:             NewStage(repo.NewStage(db)),
		ItemService:              NewItem(repo.NewItem(db)),
		baseGenerations:          &memorySiteStatsBaseGenerations{},
	}

	// refresh A reads the matrix elements, then a batch rewrites today's and refresh B starts: A must not cache its
	// stats over B's
	refreshedA := make(chan error, 1)
	go func() {
		_, err := s.RefreshShimSiteStats(context.WithValue(ctx, pauseQueriesKey{}, true), "CN")
		refreshedA <- err
	}()
	select {
	case <-hook.paused:
	case <-time.After(10 * time.Second):
		close(hook.release)
		t.Fatal("refresh A never read the matrix elements")
	}
	mustExec(t, db, "UPDATE drop_matrix_elements SET times = times * 2, quantity = quantity * 2 WHERE server = ? AND day_num = ?", "CN", today["CN"])
	want := legacySiteStatsLines(t, db, "CN")
	refreshedB := make(chan error, 1)
	go func() {
		_, err := s.RefreshShimSiteStats(ctx, "CN")
		refreshedB <- err
	}()
	// B runs after A: give B the time to finish first if it does not wait for A
	bDone := false
	select {
	case err := <-refreshedB:
		bDone = true
		if err != nil {
			t.Error(err)
		}
	case <-time.After(500 * time.Millisecond):
	}
	close(hook.release)
	if err := <-refreshedA; err != nil {
		t.Fatal(err)
	}
	if !bDone {
		if err := <-refreshedB; err != nil {
			t.Fatal(err)
		}
	}
	var cached modelv2.SiteStats
	if err := cache.ShimSiteStats.Get("CN", &cached); err != nil {
		t.Fatal(err)
	}
	assertSameLines(t, "cached after overlapping refreshes", want, siteStatsLines(&cached))
}

type queryLabelKey struct{}

// holdQueriesHook holds the queries containing query of a context labelled label with queryLabelKey, before they run
// if before is set and else after, until release is closed; held is closed when the first one is held.
type holdQueriesHook struct {
	label       string
	query       string
	before      bool
	once        sync.Once
	held        chan struct{}
	releaseOnce sync.Once
	release     chan struct{}
}

func newHoldQueriesHook(t *testing.T, db *bun.DB, label, query string, before bool) *holdQueriesHook {
	h := &holdQueriesHook{label: label, query: query, before: before, held: make(chan struct{}), release: make(chan struct{})}
	db.AddQueryHook(h)
	t.Cleanup(h.Release)
	return h
}

func (h *holdQueriesHook) Release() {
	h.releaseOnce.Do(func() { close(h.release) })
}

func (h *holdQueriesHook) hold(ctx context.Context, event *bun.QueryEvent) {
	if ctx.Value(queryLabelKey{}) != h.label || !strings.Contains(event.Query, h.query) {
		return
	}
	h.once.Do(func() { close(h.held) })
	<-h.release
}

func (h *holdQueriesHook) BeforeQuery(ctx context.Context, event *bun.QueryEvent) context.Context {
	if h.before {
		h.hold(ctx, event)
	}
	return ctx
}

func (h *holdQueriesHook) AfterQuery(ctx context.Context, event *bun.QueryEvent) {
	if !h.before {
		h.hold(ctx, event)
	}
}

func newSiteStatsForOverlapTest(t *testing.T) (*SiteStats, *bun.DB, map[string]int) {
	t.Helper()
	db := newTestDB(t)
	flushCaches(t)
	now := time.Date(2026, 10, 19, 14, 0, 0, 0, time.UTC)
	util.Now = func() time.Time { return now }
	t.Cleanup(func() { util.Now = time.Now })
	today := make(map[string]int)
	for _, server := range testServers {
		today[server] = util.GetDayNum(&now, server)
	}
	seedSiteStatsData(t, rand.New(rand.NewSource(1)), db, today, []int{-3, -1, 0})
	return &SiteStats{
		DropReportService:        NewDropReport(repo.NewDropReport(db)),
		DropMatrixElementService: NewDropMatrixElement(repo.NewDropMatrixElement(db)),
		StageService:             NewStage(repo.NewStage(db)),
		ItemService:              NewItem(repo.NewItem(db)),
		baseGenerations:          &memorySiteStatsBaseGenerations{},
	}, db, today
}

func waitHeld(t *testing.T, h *holdQueriesHook, what string) {
	t.Helper()
	select {
	case <-h.held:
	case <-time.After(10 * time.Second):
		t.Fatal(what + " never read the matrix elements")
	}
}

func TestShimSiteStatsColdRequestOverlappingRefresh(t *testing.T) {
	s, db, today := newSiteStatsForOverlapTest(t)
	ctx := context.Background()
	// the query of the recent matrix elements
	holdA := newHoldQueriesHook(t, db, "A", "day_num >=", false)

	// on a cold cache, refresh A reads the recent matrix elements, then a batch rewrites today's and a request comes: the
	// stats cached in the end are those the request returned, not older ones
	refreshedA := make(chan error, 1)
	go func() {
		_, err := s.RefreshShimSiteStats(context.WithValue(ctx, queryLabelKey{}, "A"), "CN")
		refreshedA <- err
	}()
	waitHeld(t, holdA, "refresh A")
	mustExec(t, db, "UPDATE drop_matrix_elements SET times = times * 2, quantity = quantity * 2 WHERE server = ? AND day_num = ?", "CN", today["CN"])
	requested := make(chan error, 1)
	var stats *modelv2.SiteStats
	go func() {
		var err error
		stats, err = s.GetShimSiteStats(ctx, "CN")
		requested <- err
	}()
	// give the request the time to finish first if it does not wait for A
	requestDone := false
	select {
	case err := <-requested:
		requestDone = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
	}
	holdA.Release()
	if err := <-refreshedA; err != nil {
		t.Fatal(err)
	}
	if !requestDone {
		if err := <-requested; err != nil {
			t.Fatal(err)
		}
	}
	var cached modelv2.SiteStats
	if err := cache.ShimSiteStats.Get("CN", &cached); err != nil {
		t.Fatal(err)
	}
	assertSameLines(t, "cached after the refresh and the request", siteStatsLines(stats), siteStatsLines(&cached))
}

func TestShimSiteStatsOverlappingRefreshesReadOutOfOrder(t *testing.T) {
	s, db, today := newSiteStatsForOverlapTest(t)
	ctx := context.Background()
	if _, err := s.RefreshShimSiteStats(ctx, "CN"); err != nil {
		t.Fatal(err)
	}
	// with the base cached, the refreshes only read the recent matrix elements
	holdA := newHoldQueriesHook(t, db, "A", "drop_matrix_elements", true)
	holdB := newHoldQueriesHook(t, db, "B", "drop_matrix_elements", false)

	// refresh A starts first but is held before reading the matrix elements, refresh B starts and could read them
	// before a batch rewrites today's: B must not cache stats older than those A cached
	refreshedA, refreshedB := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := s.RefreshShimSiteStats(context.WithValue(ctx, queryLabelKey{}, "A"), "CN")
		refreshedA <- err
	}()
	waitHeld(t, holdA, "refresh A")
	go func() {
		_, err := s.RefreshShimSiteStats(context.WithValue(ctx, queryLabelKey{}, "B"), "CN")
		refreshedB <- err
	}()
	// give B the time to read the matrix elements if it does not wait for A
	select {
	case <-holdB.held:
	case <-time.After(time.Second):
	}
	mustExec(t, db, "UPDATE drop_matrix_elements SET times = times * 2, quantity = quantity * 2 WHERE server = ? AND day_num = ?", "CN", today["CN"])
	want := legacySiteStatsLines(t, db, "CN")
	holdA.Release()
	if err := <-refreshedA; err != nil {
		t.Fatal(err)
	}
	holdB.Release()
	if err := <-refreshedB; err != nil {
		t.Fatal(err)
	}
	var cached modelv2.SiteStats
	if err := cache.ShimSiteStats.Get("CN", &cached); err != nil {
		t.Fatal(err)
	}
	assertSameLines(t, "cached after overlapping refreshes", want, siteStatsLines(&cached))
}
