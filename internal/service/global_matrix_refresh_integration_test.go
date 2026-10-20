package service

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"sort"
	"strconv"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
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

func TestGlobalMatrixRefresh(t *testing.T) {
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
}
