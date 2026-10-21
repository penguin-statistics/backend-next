package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/samber/lo"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	pkgcache "exusiai.dev/backend-next/internal/pkg/cache"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/util"
)

var allSourceCategories = []string{constant.SourceCategoryAll, constant.SourceCategoryManual, constant.SourceCategoryAutomated}

// newGlobalMatrixServices builds the drop and pattern matrix services with everything the worker jobs and the shim
// (global) results need.
func newGlobalMatrixServices(db *bun.DB) (*DropMatrix, *PatternMatrix) {
	conf := &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{MatrixWorkerSourceCategories: allSourceCategories, PatternMatrixLimit: 3}}
	dropInfoRepo := repo.NewDropInfo(db)
	timeRange := NewTimeRange(repo.NewTimeRange(db), dropInfoRepo)
	dropInfo := NewDropInfo(dropInfoRepo, timeRange)
	dropReport := NewDropReport(repo.NewDropReport(db))
	stage := NewStage(repo.NewStage(db))
	item := NewItem(repo.NewItem(db))
	return &DropMatrix{
			Config: conf, TimeRangeService: timeRange, DropReportService: dropReport, DropInfoService: dropInfo,
			DropMatrixElementService: NewDropMatrixElement(repo.NewDropMatrixElement(db)), StageService: stage, ItemService: item,
		}, &PatternMatrix{
			Config: conf, TimeRangeService: timeRange, DropReportService: dropReport, DropInfoService: dropInfo,
			PatternMatrixElementService: NewPatternMatrixElement(repo.NewPatternMatrixElement(db)),
			DropPatternElementService:   NewDropPatternElement(repo.NewDropPatternElement(db)), StageService: stage, ItemService: item,
		}
}

// canonicalJSON encodes the elements of a matrix one per line, sorted, as their order is not significant.
func canonicalJSON[T any](t *testing.T, elements []T) string {
	t.Helper()
	lines := make([]string, 0, len(elements))
	for _, el := range elements {
		b, err := json.Marshal(el)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	sort.Strings(lines)
	b, _ := json.Marshal(lines)
	return string(b)
}

type globalMatrixVariant struct {
	server, sourceCategory string
	flag                   bool // showClosedZones for drop matrices, showAllPatterns for pattern matrices
}

func globalMatrixVariants() []globalMatrixVariant {
	variants := make([]globalMatrixVariant, 0)
	for _, server := range testServers {
		for _, sourceCategory := range allSourceCategories {
			for _, flag := range []bool{true, false} {
				variants = append(variants, globalMatrixVariant{server, sourceCategory, flag})
			}
		}
	}
	return variants
}

func (v globalMatrixVariant) dropKey() string {
	return v.server + constant.CacheSep + strconv.FormatBool(v.flag) + constant.CacheSep + v.sourceCategory
}

func (v globalMatrixVariant) patternKey() string {
	return v.server + constant.CacheSep + v.sourceCategory + constant.CacheSep + strconv.FormatBool(v.flag)
}

// global results as requests get them
func shimGlobalMatrices(t *testing.T, dropMatrix *DropMatrix, patternMatrix *PatternMatrix) map[globalMatrixVariant][2]string {
	t.Helper()
	ctx := context.Background()
	results := make(map[globalMatrixVariant][2]string)
	for _, v := range globalMatrixVariants() {
		drop, err := dropMatrix.GetShimDropMatrix(ctx, v.server, v.flag, "", "", null.Int{}, v.sourceCategory)
		if err != nil {
			t.Fatal(err)
		}
		pattern, err := patternMatrix.GetShimPatternMatrix(ctx, v.server, null.Int{}, v.sourceCategory, v.flag)
		if err != nil {
			t.Fatal(err)
		}
		results[v] = [2]string{canonicalJSON(t, drop.Matrix), canonicalJSON(t, pattern.PatternMatrix)}
	}
	return results
}

// stale stands for global matrices calculated before the latest batch
var (
	staleDropMatrix    = modelv2.DropMatrixQueryResult{Matrix: []*modelv2.OneDropMatrixElement{{StageID: "stale"}}}
	stalePatternMatrix = modelv2.PatternMatrixQueryResult{PatternMatrix: []*modelv2.OnePatternMatrixElement{{StageID: "stale"}}}
)

func setStaleGlobalMatrices() {
	for _, v := range globalMatrixVariants() {
		cache.ShimGlobalDropMatrix.Set(v.dropKey(), staleDropMatrix, time.Hour)
		cache.ShimGlobalPatternMatrix.Set(v.patternKey(), stalePatternMatrix, time.Hour)
	}
}

func isStale(t *testing.T, v globalMatrixVariant) (dropStale, patternStale bool) {
	t.Helper()
	var drop modelv2.DropMatrixQueryResult
	if err := cache.ShimGlobalDropMatrix.Get(v.dropKey(), &drop); err == nil {
		dropStale = len(drop.Matrix) == 1 && drop.Matrix[0].StageID == "stale"
	} else if !errors.Is(err, pkgcache.ErrNotFound) {
		t.Fatal(err)
	}
	var pattern modelv2.PatternMatrixQueryResult
	if err := cache.ShimGlobalPatternMatrix.Get(v.patternKey(), &pattern); err == nil {
		patternStale = len(pattern.PatternMatrix) == 1 && pattern.PatternMatrix[0].StageID == "stale"
	} else if !errors.Is(err, pkgcache.ErrNotFound) {
		t.Fatal(err)
	}
	return dropStale, patternStale
}

// newGlobalMatrixTestData seeds matrix data, stages and items, and runs the worker's jobs over the seeded days
// (checking that a batch keeps the cached global matrices).
func newGlobalMatrixTestData(t *testing.T) (*bun.DB, *DropMatrix, *PatternMatrix) {
	t.Helper()
	db := newTestDB(t)
	ctx := context.Background()
	mustExec(t, db, `CREATE TABLE pattern_matrix_elements (
		element_id bigserial PRIMARY KEY, stage_id integer NOT NULL, pattern_id bigint NOT NULL, quantity integer NOT NULL,
		times integer NOT NULL, server server NOT NULL, source_category character varying(32) DEFAULT 'all' NOT NULL,
		start_time timestamp with time zone, end_time timestamp with time zone, day_num integer NOT NULL)`)
	seedMatrixData(t, rand.New(rand.NewSource(1)), db)
	for stageId := 1; stageId <= 6; stageId++ {
		mustExec(t, db, "INSERT INTO stages (stage_id, ark_stage_id, zone_id, stage_type) VALUES (?, ?, 1, 'MAIN')", stageId, "stage_"+strconv.Itoa(stageId))
	}
	for itemId := 10; itemId <= 15; itemId++ {
		mustExec(t, db, "INSERT INTO items (item_id, ark_item_id, type, sort_id) VALUES (?, ?, 'MATERIAL', ?)", itemId, "item_"+strconv.Itoa(itemId), itemId)
	}
	dropMatrix, patternMatrix := newGlobalMatrixServices(db)
	t.Cleanup(func() { util.Now = time.Now })

	// the worker's batches over the seeded days; the global matrices cached before a batch are kept by it
	for day := 0; day < 5; day++ {
		now := testBaseTime.Add(time.Duration(day)*24*time.Hour + 20*time.Hour)
		util.Now = func() time.Time { return now }
		setStaleGlobalMatrices()
		for _, server := range testServers {
			if err := dropMatrix.RunCalcDropMatrixJob(ctx, server); err != nil {
				t.Fatal(err)
			}
			if err := patternMatrix.RunCalcPatternMatrixJob(ctx, server); err != nil {
				t.Fatal(err)
			}
		}
		for _, v := range globalMatrixVariants() {
			if dropStale, patternStale := isStale(t, v); !dropStale || !patternStale {
				t.Fatalf("day %d %+v: a batch dropped the cached global matrices (drop kept %v, pattern kept %v)", day, v, dropStale, patternStale)
			}
		}
	}
	return db, dropMatrix, patternMatrix
}

func TestGlobalMatrixRefresh(t *testing.T) {
	db, dropMatrix, patternMatrix := newGlobalMatrixTestData(t)
	ctx := context.Background()

	// what requests calculate from an empty cache
	flushCaches(t)
	want := shimGlobalMatrices(t, dropMatrix, patternMatrix)
	nonEmpty := 0
	for _, results := range want {
		if results[0] != "[]" && results[1] != "[]" {
			nonEmpty++
		}
	}
	if nonEmpty < len(want)/2 {
		t.Fatalf("only %d of %d global matrices have elements: the data does not exercise the refresh", nonEmpty, len(want))
	}

	// a refresh replaces the stale matrices with exactly those
	flushCaches(t)
	setStaleGlobalMatrices()
	for _, server := range testServers {
		if err := dropMatrix.RefreshGlobalDropMatrixCaches(ctx, server); err != nil {
			t.Fatal(err)
		}
		if err := patternMatrix.RefreshGlobalPatternMatrixCaches(ctx, server); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range globalMatrixVariants() {
		var lastModified time.Time
		if err := cache.LastModifiedTime.Get("[shimGlobalDropMatrix#server|showClosedZones|sourceCategory:"+v.dropKey()+"]", &lastModified); err != nil {
			t.Errorf("%+v: no last modified time for the refreshed drop matrix", v)
		}
		if err := cache.LastModifiedTime.Get("[shimGlobalPatternMatrix#server|sourceCategory|showAllPatterns:"+v.patternKey()+"]", &lastModified); err != nil {
			t.Errorf("%+v: no last modified time for the refreshed pattern matrix", v)
		}
	}
	got := shimGlobalMatrices(t, dropMatrix, patternMatrix)
	for v, w := range want {
		if got[v][0] != w[0] {
			t.Errorf("%+v: refreshed drop matrix differs from the one calculated on request\ngot  %.300s\nwant %.300s", v, got[v][0], w[0])
		}
		if got[v][1] != w[1] {
			t.Errorf("%+v: refreshed pattern matrix differs from the one calculated on request\ngot  %.300s\nwant %.300s", v, got[v][1], w[1])
		}
	}

	// a failed refresh drops the stale matrices, so that requests recalculate them
	setStaleGlobalMatrices()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	flushCaches(t) // the time ranges must be queried, with the cancelled context
	setStaleGlobalMatrices()
	if err := dropMatrix.RefreshGlobalDropMatrixCaches(cancelled, "CN"); err == nil {
		t.Fatal("refreshing with a cancelled context succeeded")
	}
	if err := patternMatrix.RefreshGlobalPatternMatrixCaches(cancelled, "CN"); err == nil {
		t.Fatal("refreshing with a cancelled context succeeded")
	}
	for _, v := range globalMatrixVariants() {
		dropStale, patternStale := isStale(t, v)
		if v.server == "CN" && (dropStale || patternStale) {
			t.Errorf("%+v: a failed refresh kept the stale matrices (drop %v, pattern %v)", v, dropStale, patternStale)
		}
		if v.server != "CN" && (!dropStale || !patternStale) {
			t.Errorf("%+v: a failed refresh of CN dropped the matrices of another server", v)
		}
	}

	// a worker or admin rewrite of a stage's pattern elements (delete, then insert) in progress: the single statement
	// reads one state consistently, without the stage while it is deleted
	before, err := patternMatrix.calcGlobalPatternMatrices(ctx, "CN", allSourceCategories)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, "CREATE TABLE rewritten AS SELECT * FROM pattern_matrix_elements WHERE server = 'CN' AND stage_id = 1")
	mustExec(t, db, "DELETE FROM pattern_matrix_elements WHERE server = 'CN' AND stage_id = 1")
	during, err := patternMatrix.calcGlobalPatternMatrices(ctx, "CN", allSourceCategories)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, "INSERT INTO pattern_matrix_elements SELECT * FROM rewritten")
	after, err := patternMatrix.calcGlobalPatternMatrices(ctx, "CN", allSourceCategories)
	if err != nil {
		t.Fatal(err)
	}
	withoutStage1 := func(elements []*model.OnePatternMatrixElement) []*model.OnePatternMatrixElement {
		return lo.Filter(elements, func(el *model.OnePatternMatrixElement, _ int) bool { return el.StageID != 1 })
	}
	for _, sourceCategory := range allSourceCategories {
		if len(withoutStage1(before[sourceCategory].PatternMatrix)) == len(before[sourceCategory].PatternMatrix) {
			t.Fatalf("%s: stage 1 has no pattern matrix to rewrite", sourceCategory)
		}
		if canonicalJSON(t, during[sourceCategory].PatternMatrix) != canonicalJSON(t, withoutStage1(before[sourceCategory].PatternMatrix)) {
			t.Errorf("%s: during the rewrite, got other than the matrix without stage 1", sourceCategory)
		}
		if canonicalJSON(t, after[sourceCategory].PatternMatrix) != canonicalJSON(t, before[sourceCategory].PatternMatrix) {
			t.Errorf("%s: after the rewrite, got other than before", sourceCategory)
		}
	}
}

func TestGlobalMatricesMatchLegacy(t *testing.T) {
	db, dropMatrix, patternMatrix := newGlobalMatrixTestData(t)
	ctx := context.Background()
	compared := 0
	for _, server := range testServers {
		drops, err := dropMatrix.calcGlobalDropMatrices(ctx, server, allSourceCategories)
		if err != nil {
			t.Fatal(err)
		}
		patterns, err := patternMatrix.calcGlobalPatternMatrices(ctx, server, allSourceCategories)
		if err != nil {
			t.Fatal(err)
		}
		for _, sourceCategory := range allSourceCategories {
			what := server + " " + sourceCategory
			wantDrop, err := legacyCalcGlobalDropMatrix(ctx, db, dropMatrix, server, sourceCategory)
			if err != nil {
				t.Fatal(err)
			}
			compared += assertSameLines(t, what+" drop", globalDropMatrixLines(wantDrop), globalDropMatrixLines(drops[sourceCategory]))
			wantPattern, err := legacyCalcGlobalPatternMatrix(ctx, db, patternMatrix, server, sourceCategory)
			if err != nil {
				t.Fatal(err)
			}
			compared += assertSameLines(t, what+" pattern", globalPatternMatrixLines(wantPattern), globalPatternMatrixLines(patterns[sourceCategory]))

			// a request calculates the matrix of a single source category
			single, err := dropMatrix.calcGlobalDropMatrixUncached(ctx, server, sourceCategory)
			if err != nil {
				t.Fatal(err)
			}
			assertSameLines(t, what+" drop of a single category", globalDropMatrixLines(wantDrop), globalDropMatrixLines(single))
		}
	}
	requireCompared(t, compared, 100)
}

func globalDropMatrixLines(result *model.DropMatrixQueryResult) []string {
	lines := make([]string, 0, len(result.Matrix))
	for _, el := range result.Matrix {
		lines = append(lines, fmt.Sprintf("%d|%d|%d|%d|%v|%s|%s", el.StageID, el.ItemID, el.Times, el.Quantity, el.StdDev,
			el.TimeRange.StartTime.UTC().Format(time.RFC3339Nano), el.TimeRange.EndTime.UTC().Format(time.RFC3339Nano)))
	}
	sort.Strings(lines) // the order of the elements is not significant
	return lines
}

func globalPatternMatrixLines(result *model.PatternMatrixQueryResult) []string {
	lines := make([]string, 0, len(result.PatternMatrix))
	for _, el := range result.PatternMatrix {
		lines = append(lines, fmt.Sprintf("%d|%d|%d|%d|%s|%s", el.StageID, el.PatternID, el.Times, el.Quantity,
			el.TimeRange.StartTime.UTC().Format(time.RFC3339Nano), el.TimeRange.EndTime.UTC().Format(time.RFC3339Nano)))
	}
	sort.Strings(lines) // the order of the elements is not significant
	return lines
}

// requestWhileRefreshWaits starts refresh, holds a lock on table until the refresh waits for it, and runs request
// meanwhile: request must not wait for the refresh, as it would if the refresh dropped the cache first.
func requestWhileRefreshWaits(t *testing.T, db *bun.DB, table string, refresh func() error, request func()) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	refreshed := make(chan error, 1)
	go func() { refreshed <- refresh() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := db.NewRaw("SELECT count(*) FROM pg_locks WHERE relation = ?::regclass AND NOT granted", table).Scan(ctx, &waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the refresh never read %s", table)
		}
		time.Sleep(10 * time.Millisecond)
	}

	requested := make(chan struct{})
	go func() {
		defer close(requested)
		request()
	}()
	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		t.Error("a request waited for the refresh")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	<-requested
	if err := <-refreshed; err != nil {
		t.Fatal(err)
	}
}

func TestShimTrendRefresh(t *testing.T) {
	db, dropMatrix, _ := newGlobalMatrixTestData(t)
	ctx := context.Background()
	trend := &Trend{StageService: dropMatrix.StageService, ItemService: dropMatrix.ItemService, DropMatrixElementService: dropMatrix.DropMatrixElementService}
	stale := modelv2.TrendQueryResult{Trend: map[string]*modelv2.StageTrend{"stale": {}}}
	isStale := func(server string) bool {
		var cached modelv2.TrendQueryResult
		if err := cache.ShimTrend.Get(server, &cached); err != nil {
			if !errors.Is(err, pkgcache.ErrNotFound) {
				t.Fatal(err)
			}
			return false
		}
		_, ok := cached.Trend["stale"]
		return ok
	}

	// the worker's drop matrix job keeps the cached trend
	cache.ShimTrend.Set("CN", stale, time.Hour)
	if err := dropMatrix.RunCalcDropMatrixJob(ctx, "CN"); err != nil {
		t.Fatal(err)
	}
	if !isStale("CN") {
		t.Fatal("the drop matrix job dropped the cached trend")
	}

	// the trend covers the days before the actual date: replace the seeded elements with some of those days
	mustExec(t, db, "DELETE FROM drop_matrix_elements")
	now := time.Now()
	r := rand.New(rand.NewSource(1))
	for _, server := range testServers {
		today := util.GetDayNum(&now, server)
		for _, dayNum := range []int{today - 40, today - 7, today - 1, today} {
			for stageId := 1; stageId <= 6; stageId++ {
				times := r.Intn(100)
				for itemId := 10; itemId <= 15; itemId++ {
					if r.Intn(2) == 0 {
						continue
					}
					for _, sourceCategory := range allSourceCategories {
						mustExec(t, db, `INSERT INTO drop_matrix_elements (stage_id, item_id, quantity, times, server, source_category, day_num)
							VALUES (?, ?, ?, ?, ?, ?, ?)`, stageId, itemId, r.Intn(200), times, server, sourceCategory, dayNum)
					}
				}
			}
		}
	}

	// what requests calculate from an empty cache
	flushCaches(t)
	want := make(map[string]string)
	for _, server := range testServers {
		result, err := trend.GetShimTrend(ctx, server)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Trend) == 0 {
			t.Fatalf("%s: the trend is empty: the data does not exercise the refresh", server)
		}
		b, _ := json.Marshal(result) // maps are encoded in key order
		want[server] = string(b)
	}

	// a refresh replaces the stale trend with exactly that
	flushCaches(t)
	for _, server := range testServers {
		cache.ShimTrend.Set(server, stale, time.Hour)
		if err := trend.RefreshShimTrend(ctx, server); err != nil {
			t.Fatal(err)
		}
		var lastModified time.Time
		if err := cache.LastModifiedTime.Get("[shimTrend#server:"+server+"]", &lastModified); err != nil {
			t.Errorf("%s: no last modified time for the refreshed trend", server)
		}
		var cached modelv2.TrendQueryResult
		if err := cache.ShimTrend.Get(server, &cached); err != nil {
			t.Fatal(err)
		}
		if b, _ := json.Marshal(cached); string(b) != want[server] {
			t.Errorf("%s: refreshed trend differs from the one calculated on request\ngot  %.300s\nwant %.300s", server, b, want[server])
		}
	}

	// requests get the stale trend while a refresh calculates, without waiting for it
	cache.ShimTrend.Set("CN", stale, time.Hour)
	requestWhileRefreshWaits(t, db, "drop_matrix_elements", func() error { return trend.RefreshShimTrend(ctx, "CN") }, func() {
		result, err := trend.GetShimTrend(ctx, "CN")
		if err != nil {
			t.Error(err)
			return
		}
		if _, ok := result.Trend["stale"]; !ok {
			t.Error("a request during a refresh did not get the cached trend")
		}
	})
	if isStale("CN") {
		t.Error("the refresh kept the stale trend")
	}

	// a failed refresh drops the stale trend, so that requests recalculate it, and only that of its server
	for _, server := range testServers {
		cache.ShimTrend.Set(server, stale, time.Hour)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := trend.RefreshShimTrend(cancelled, "CN"); err == nil {
		t.Fatal("refreshing with a cancelled context succeeded")
	}
	if isStale("CN") {
		t.Error("a failed refresh kept the stale trend")
	}
	if !isStale("US") {
		t.Error("a failed refresh of CN dropped the trend of another server")
	}

	// a refresh that panics (an element of an item the items map does not have yet, e.g. during a metadata update)
	// drops the stale trend too, and only that of its server
	for _, server := range testServers {
		cache.ShimTrend.Set(server, stale, time.Hour)
	}
	today := util.GetDayNum(&now, "CN")
	mustExec(t, db, `INSERT INTO drop_matrix_elements (stage_id, item_id, quantity, times, server, source_category, day_num)
		VALUES (1, 999, 1, 1, 'CN', ?, ?)`, constant.SourceCategoryAll, today)
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("the refresh did not panic: the test does not exercise a panic")
			}
		}()
		_ = trend.RefreshShimTrend(ctx, "CN")
	}()
	if isStale("CN") {
		t.Error("a refresh that panicked kept the stale trend")
	}
	if !isStale("US") {
		t.Error("a refresh of CN that panicked dropped the trend of another server")
	}
}

// pauseAfterReading pauses the first query of table made with a context carrying it, once it has run, until released.
type pauseAfterReading struct {
	table   string
	once    sync.Once
	read    chan struct{}
	release chan struct{}
}

type pauseAfterReadingKey struct{}

func (p *pauseAfterReading) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

func (p *pauseAfterReading) AfterQuery(ctx context.Context, event *bun.QueryEvent) {
	if ctx.Value(pauseAfterReadingKey{}) != p || !strings.Contains(event.Query, p.table) {
		return
	}
	p.once.Do(func() {
		close(p.read)
		<-p.release
	})
}

// A refresh that read the elements before an admin recalculation of them and the purge of the trend must not cache
// its trend over the one a request calculates from the recalculated elements.
func TestShimTrendRefreshAfterAPurge(t *testing.T) {
	db, dropMatrix, _ := newGlobalMatrixTestData(t)
	ctx := context.Background()
	trend := &Trend{StageService: dropMatrix.StageService, ItemService: dropMatrix.ItemService, DropMatrixElementService: dropMatrix.DropMatrixElementService}
	trendJSON := func(result *modelv2.TrendQueryResult) string {
		b, _ := json.Marshal(result) // maps are encoded in key order
		return string(b)
	}

	// the trend covers the days before the actual date
	now := time.Now()
	today := util.GetDayNum(&now, "CN")
	mustExec(t, db, "DELETE FROM drop_matrix_elements")
	mustExec(t, db, `INSERT INTO drop_matrix_elements (stage_id, item_id, quantity, times, server, source_category, day_num)
		VALUES (1, 10, 1, 10, 'CN', ?, ?)`, constant.SourceCategoryAll, today)
	flushCaches(t)
	before, err := trend.GetShimTrend(ctx, "CN")
	if err != nil {
		t.Fatal(err)
	}

	pause := &pauseAfterReading{table: "drop_matrix_elements", read: make(chan struct{}), release: make(chan struct{})}
	db.AddQueryHook(pause)
	refreshed := make(chan error, 1)
	go func() {
		refreshed <- trend.RefreshShimTrend(context.WithValue(ctx, pauseAfterReadingKey{}, pause), "CN")
	}()
	select {
	case <-pause.read:
	case <-time.After(10 * time.Second):
		t.Fatal("the refresh never read the elements")
	}

	// the admin recalculation, and the purge of the trend, while the refresh holds the elements it read before
	mustExec(t, db, "UPDATE drop_matrix_elements SET quantity = 2 WHERE server = 'CN'")
	if err := cache.Delete("shimTrend#server", null.StringFrom("CN")); err != nil {
		t.Fatal(err)
	}
	requested := make(chan *modelv2.TrendQueryResult, 1)
	go func() {
		result, err := trend.GetShimTrend(ctx, "CN")
		if err != nil {
			t.Error(err)
		}
		requested <- result
	}()
	time.Sleep(100 * time.Millisecond) // for the request to wait for the refresh, or to calculate meanwhile
	close(pause.release)
	if err := <-refreshed; err != nil {
		t.Fatal(err)
	}
	during := <-requested

	cached, err := trend.GetShimTrend(ctx, "CN")
	if err != nil {
		t.Fatal(err)
	}
	flushCaches(t)
	want, err := trend.GetShimTrend(ctx, "CN")
	if err != nil {
		t.Fatal(err)
	}
	if trendJSON(want) == trendJSON(before) {
		t.Fatal("the recalculation did not change the trend: the test does not exercise the purge")
	}
	if during != nil && trendJSON(during) != trendJSON(want) {
		t.Errorf("the request after the purge got\n%s\nwant the recalculated\n%s", trendJSON(during), trendJSON(want))
	}
	if trendJSON(cached) != trendJSON(want) {
		t.Errorf("cached after the refresh\n%s\nwant the recalculated\n%s", trendJSON(cached), trendJSON(want))
	}
}
