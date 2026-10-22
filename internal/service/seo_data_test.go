package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/fx"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
)

func TestSEOI18nFallsBackLikeTheFrontend(t *testing.T) {
	s := SEOI18n{"zh": "固源岩", "en": "Orirock Cube", "ja": ""}
	for lang, want := range map[string]string{"zh": "固源岩", "en": "Orirock Cube", "ja": "Orirock Cube", "ko": "Orirock Cube"} {
		if got := s.In(lang); got != want {
			t.Errorf("In(%s) = %q, want %q", lang, got, want)
		}
	}
	if got := (SEOI18n{"zh": "昨日海"}).In("ko"); got != "昨日海" {
		t.Errorf("zh only: %q", got)
	}
	if got := SEOI18n(nil).In("en"); got != "" {
		t.Errorf("nil: %q", got)
	}
}

func TestNewSEOCatalog(t *testing.T) {
	c := NewSEOCatalog(
		[]*model.Item{
			{ArkItemID: "30013", Type: "MATERIAL", SortID: 2, Name: []byte(`{"zh":"固源岩组","en":"Orirock Cluster"}`), Existence: []byte(`{"CN":{"exist":true},"US":{"exist":false}}`), Group: null.StringFrom("orirock")},
			{ArkItemID: "30012", Type: "MATERIAL", SortID: 1, Name: []byte(`{"zh":"固源岩"}`), Existence: []byte(`not json`)},
		},
		[]*model.Stage{
			{ArkStageID: "main_01-07", StageID: 2, ZoneID: 1, Code: []byte(`{"zh":"1-7"}`), Sanity: null.IntFrom(6), Existence: []byte(`{"CN":{"exist":true,"openTime":1556676000000}}`)},
			{ArkStageID: "orphan", StageID: 1, ZoneID: 99},
		},
		[]*model.Zone{{ArkZoneID: "main_1", ZoneID: 1, Category: "MAINLINE", Name: []byte(`{"en":"Chapter 1"}`)}},
	)
	if c.ItemList[0].ArkID != "30012" || c.StageList[0].ArkID != "orphan" {
		t.Error("lists not ordered by sort ID and stage ID")
	}
	cluster := c.Items["30013"]
	if cluster.Name.In("ja") != "Orirock Cluster" || !cluster.Existence.In("CN") || cluster.Existence.In("US") || cluster.Group != "orirock" {
		t.Errorf("item: %+v", cluster)
	}
	if c.Items["30012"].Existence.In("CN") {
		t.Error("malformed existence exists")
	}
	stage := c.Stages["main_01-07"]
	if stage.Zone == nil || stage.Zone.ArkID != "main_1" || stage.Existence["CN"].OpenTime.Int64 != 1556676000000 {
		t.Errorf("stage: %+v", stage)
	}
	if c.Stages["orphan"].Zone != nil {
		t.Error("stage of an unknown zone has a zone")
	}
}

func TestNewSEOMatrixOrdersLikeTheFrontend(t *testing.T) {
	rows := []*modelv2.OneDropMatrixElement{
		{StageID: "a", ItemID: "x", Times: 100, Quantity: 50},
		{StageID: "b", ItemID: "x", Times: 1000, Quantity: 500},
		{StageID: "c", ItemID: "x", Times: 10, Quantity: 23},
		{StageID: "d", ItemID: "x", Times: 0, Quantity: 0},
		{StageID: "0", ItemID: "x", Times: 100, Quantity: 50},
		{StageID: "a", ItemID: "y", Times: 100, Quantity: 1},
	}
	m := NewSEOMatrix("CN", rows, time.Unix(1, 0))
	var order []string
	for _, r := range m.ByItem["x"] {
		order = append(order, r.StageID)
	}
	if got := order; len(got) != 5 || got[0] != "c" || got[1] != "b" || got[2] != "0" || got[3] != "a" || got[4] != "d" {
		t.Errorf("order %v, want [c b 0 a d]", got)
	}
	if r := m.ByItem["x"][0]; r.Rate != 2.3 {
		t.Errorf("rate %v, want 2.3 (above 1 for more than one drop per run)", r.Rate)
	}
	if got := m.ByStage["a"]; len(got) != 2 || got[0].ItemID != "x" || got[1].Rate != 0.01 {
		t.Errorf("stage a: %+v", got)
	}
}

// hookCounter is an fx.Lifecycle that counts the hooks appended to it
type hookCounter int

func (h *hookCounter) Append(fx.Hook) { *h++ }

func TestSEODataLoadsForThePagesOrTheMCPServer(t *testing.T) {
	for _, tc := range []struct {
		pages, mcp bool
		hooks      int
	}{
		{pages: false, mcp: false, hooks: 0},
		{pages: true, mcp: false, hooks: 1},
		{pages: false, mcp: true, hooks: 1},
		{pages: true, mcp: true, hooks: 1},
	} {
		var lc hookCounter
		NewSEOData(&lc, &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{SEOPagesEnabled: tc.pages, MCPEnabled: tc.mcp}}, nil, nil, nil, nil)
		if int(lc) != tc.hooks {
			t.Errorf("pages %v, MCP %v: %d hooks, want %d", tc.pages, tc.mcp, lc, tc.hooks)
		}
	}
}

func TestSEODataMatrixPeeksTheCache(t *testing.T) {
	cache.Initialize()
	d := &SEOData{matrices: make(map[string]*seoMatrixMemo)}
	key := "KR|true|all"
	lmKey := "[shimGlobalDropMatrix#server|showClosedZones|sourceCategory:" + key + "]"
	t.Cleanup(func() {
		_ = cache.ShimGlobalDropMatrix.Delete(key)
		_ = cache.LastModifiedTime.Delete(lmKey)
	})
	_ = cache.ShimGlobalDropMatrix.Delete(key)

	if d.Matrix("KR") != nil {
		t.Fatal("matrix without a cached one")
	}

	rows := []*modelv2.OneDropMatrixElement{{StageID: "s", ItemID: "i", Times: 2, Quantity: 1}}
	cache.ShimGlobalDropMatrix.Set(key, modelv2.DropMatrixQueryResult{Matrix: rows}, time.Hour)
	cache.LastModifiedTime.Set(lmKey, time.Unix(100, 0), 0)
	m := d.Matrix("KR")
	if m == nil || len(m.ByItem["i"]) != 1 || !m.LastModified.Equal(time.Unix(100, 0)) {
		t.Fatalf("matrix %+v", m)
	}
	if d.Matrix("KR") != m {
		t.Error("index built again for the same cached matrix")
	}

	// the worker refreshed the matrix
	cache.ShimGlobalDropMatrix.Set(key, modelv2.DropMatrixQueryResult{Matrix: []*modelv2.OneDropMatrixElement{{StageID: "s", ItemID: "j", Times: 2, Quantity: 2}}}, time.Hour)
	cache.LastModifiedTime.Set(lmKey, time.Unix(200, 0), 0)
	if m2 := d.Matrix("KR"); m2 == m || len(m2.ByItem["j"]) != 1 || len(m2.ByItem["i"]) != 0 {
		t.Errorf("matrix not rebuilt after refresh: %+v", m2)
	}
}

func TestSEODataMatrixIsNeverNewerThanItsRows(t *testing.T) {
	cache.Initialize()
	d := &SEOData{matrices: make(map[string]*seoMatrixMemo)}
	key := "JP|true|all"
	lmKey := "[shimGlobalDropMatrix#server|showClosedZones|sourceCategory:" + key + "]"
	t.Cleanup(func() {
		_ = cache.ShimGlobalDropMatrix.Delete(key)
		_ = cache.LastModifiedTime.Delete(lmKey)
	})
	// the version of a matrix is the times of its row; published as the worker does
	const n = 2000
	times := make([]time.Time, n)
	publish := func(v int) {
		result := modelv2.DropMatrixQueryResult{Matrix: []*modelv2.OneDropMatrixElement{{StageID: "s", ItemID: "i", Times: v}}}
		cache.SetLastModifiedTime(lmKey, result, func() {
			cache.ShimGlobalDropMatrix.Set(key, result, time.Hour)
		})
		times[v] = cache.GetLastModifiedTime(lmKey)
	}
	publish(0)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for v := 1; v < n; v++ {
			publish(v)
		}
	}()
	type seen struct {
		v  int
		at time.Time
	}
	seens := make([][]seen, 4)
	var wg sync.WaitGroup
	for i := range seens {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for running := true; running; {
				select {
				case <-done:
					running = false
				default:
				}
				m := d.Matrix("JP")
				seens[i] = append(seens[i], seen{v: m.ByItem["i"][0].Times, at: m.LastModified})
			}
		}(i)
	}
	wg.Wait()
	for _, ss := range seens {
		for _, s := range ss {
			if s.at.After(times[s.v]) {
				t.Fatalf("matrix %d has the time %v of a newer one, its own is %v", s.v, s.at, times[s.v])
			}
		}
	}
}

func TestSEODataKeepsTheCatalogWhileUnchanged(t *testing.T) {
	cache.Initialize()
	t.Cleanup(func() {
		_ = cache.Items.Delete()
		_ = cache.Stages.Delete()
		_ = cache.Zones.Delete()
	})
	items := func(name string) []*model.Item {
		return []*model.Item{{ArkItemID: "30012", Type: "MATERIAL", Name: []byte(`{"en":"` + name + `"}`)}}
	}
	cache.Items.Set(items("Orirock Cube"), time.Hour)
	cache.Stages.Set([]*model.Stage{{ArkStageID: "main_01-07", StageID: 1, ZoneID: 1}}, time.Hour)
	cache.Zones.Set([]*model.Zone{{ArkZoneID: "main_1", ZoneID: 1}}, time.Hour)
	d := &SEOData{itemService: &Item{}, stageService: &Stage{}, zoneService: &Zone{}}
	ctx := context.Background()
	if err := d.RefreshCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	first := d.Catalog()
	if !first.ChangedAt.IsZero() {
		t.Errorf("first catalog changed at %v, want zero", first.ChangedAt)
	}
	if err := d.RefreshCatalog(ctx); err != nil || d.Catalog() != first {
		t.Fatalf("catalog replaced while the cached rows are the same: %v", err)
	}
	// the cache expired and the same rows were loaded again
	cache.Items.Set(items("Orirock Cube"), time.Hour)
	if err := d.RefreshCatalog(ctx); err != nil || d.Catalog() != first {
		t.Fatalf("catalog replaced for equal rows: %v", err)
	}
	cache.Items.Set(items("Orirock Cube II"), time.Hour)
	before := time.Now()
	if err := d.RefreshCatalog(ctx); err != nil || d.Catalog() == first || d.Catalog().Items["30012"].Name.In("en") != "Orirock Cube II" {
		t.Fatalf("catalog not replaced for changed rows: %v", err)
	}
	if at := d.Catalog().ChangedAt; at.Before(before) || at.After(time.Now()) {
		t.Errorf("replaced catalog changed at %v, want the time it was replaced", at)
	}
}

func TestSEODataWarmsOnlyMissingMatrices(t *testing.T) {
	cache.Initialize()
	for _, server := range []string{"CN", "US", "JP", "KR"} {
		key := server + "|true|all"
		cache.ShimGlobalDropMatrix.Set(key, modelv2.DropMatrixQueryResult{}, time.Hour)
		t.Cleanup(func() { _ = cache.ShimGlobalDropMatrix.Delete(key) })
	}
	// with every matrix cached, nothing is calculated: the nil service is never called
	d := &SEOData{matrices: make(map[string]*seoMatrixMemo)}
	d.warmMatrices(context.Background())
}

func TestSEODataRecalculatesMissingMatrices(t *testing.T) {
	cache.Initialize()
	servers := []string{"CN", "US", "JP", "KR"}
	for _, server := range servers {
		key := server + "|true|all"
		t.Cleanup(func() { _ = cache.ShimGlobalDropMatrix.Delete(key) })
	}

	var calls []string
	failCN := true
	d := &SEOData{matrices: make(map[string]*seoMatrixMemo)}
	d.calcMatrix = func(_ context.Context, server string) error {
		calls = append(calls, server)
		if server == "CN" && failCN {
			return errors.New("transient")
		}
		cache.ShimGlobalDropMatrix.Set(server+"|true|all", modelv2.DropMatrixQueryResult{}, time.Hour)
		return nil
	}
	pass := func(want ...string) {
		t.Helper()
		calls = nil
		d.warmMatrices(context.Background())
		if len(calls) != len(want) {
			t.Fatalf("calculated %v, want %v", calls, want)
		}
		for i := range want {
			if calls[i] != want[i] {
				t.Fatalf("calculated %v, want %v", calls, want)
			}
		}
	}

	pass(servers...)
	// the failed server is calculated again in the next pass, the others are left cached
	failCN = false
	pass("CN")
	pass()
	// a cached matrix that expired or was dropped is calculated again
	_ = cache.ShimGlobalDropMatrix.Delete("JP|true|all")
	pass("JP")
}

func TestSEODataWarmsAtMostOncePerIntervalAndNeverTwiceAtOnce(t *testing.T) {
	cache.Initialize()
	t.Cleanup(func() { _ = cache.ShimGlobalDropMatrix.Delete("CN|true|all") })

	var (
		mu      sync.Mutex
		passes  int
		release = make(chan struct{})
		done    = make(chan struct{}, 4)
	)
	d := &SEOData{matrices: make(map[string]*seoMatrixMemo)}
	d.calcMatrix = func(_ context.Context, server string) error {
		if server == "CN" {
			mu.Lock()
			passes++
			mu.Unlock()
			<-release
			done <- struct{}{}
		}
		return errors.New("not cached")
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return passes
	}
	waitIdle := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for d.warming.Load() {
			if time.Now().After(deadline) {
				t.Fatal("the pass did not end")
			}
			time.Sleep(time.Millisecond)
		}
	}

	start := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	d.maybeWarm(ctx, start)
	for count() == 0 {
		time.Sleep(time.Millisecond)
	}
	// a pass that is still running is not overlapped, even after the interval
	d.maybeWarm(ctx, start.Add(2*seoWarmInterval))
	close(release)
	<-done
	waitIdle()
	if got := count(); got != 1 {
		t.Fatalf("passes = %d, want 1", got)
	}
	// within the interval after the last pass started, no pass starts
	d.lastWarm = start
	d.maybeWarm(ctx, start.Add(seoWarmInterval-time.Second))
	waitIdle()
	if got := count(); got != 1 {
		t.Fatalf("passes = %d within the interval, want 1", got)
	}
	// after it, the missing matrix is calculated again
	d.maybeWarm(ctx, start.Add(seoWarmInterval))
	<-done
	waitIdle()
	if got := count(); got != 2 {
		t.Fatalf("passes = %d after the interval, want 2", got)
	}
}
