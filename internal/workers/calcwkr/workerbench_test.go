//go:build workerbench

// Worker (calcwkr) benchmark harness. Runs stats batches through the worker itself (lock,
// microtasks, every server) with the clock pinned so runs are reproducible, and without the
// configured separation sleeps. Like the long-running worker, the process runs a batch first; then
// every in-process cache a 30-minute batch interval would have expired is flushed (all of them,
// except the ones in longLivedCaches) and the second batch is timed. Prints per-phase timings and
// a SHA-256 over the rows the batch wrote plus the site stats it produced, so behavioural changes
// are caught.
//
// NOTE: this WRITES to drop_matrix_elements / pattern_matrix_elements for the pinned day
// (delete + re-insert, as the worker does). Use scripts/workerbench/bench.py, which backs the
// affected rows up first and keeps the tables vacuumed between runs.
//
// Env: WORKERBENCH_NOW (RFC3339, default 2026-10-19T14:00:00Z), WORKERBENCH_DUMP (canonical output path),
// WORKERBENCH_PRINT_DAYS (only print the days of every server a batch may rewrite).
package calcwkr_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/uptrace/bun"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/app"
	"exusiai.dev/backend-next/internal/app/appcontext"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/service"
	"exusiai.dev/backend-next/internal/util"
	"exusiai.dev/backend-next/internal/workers/calcwkr"
)

const defaultNow = "2026-10-19T14:00:00Z"

var phases = []string{"dropMatrix", "patternMatrix", "siteStats"}

// longLivedCaches are caches that outlive the worker's batch interval and are kept between the two batches.
var longLivedCaches = map[string]bool{
	"siteStatsBase#server":                       true, // 6 hrs
	"globalDropMatrixBase#server|sourceCategory": true, // rebuilt after GlobalMatrixBaseMaxAge
}

func TestWorkerBench(t *testing.T) {
	var (
		db        *bun.DB
		deps      calcwkr.WorkerDeps
		siteStats *service.SiteStats
	)
	// no background workers: the scheduled calcwkr batch or the report consumer would write to the database
	// during the measurement
	fxApp := fx.New(append(app.OptionsWithoutWorkers(appcontext.Declare(appcontext.EnvCLI)),
		fx.Populate(&db, &siteStats),
		fx.Invoke(func(d calcwkr.WorkerDeps) { deps = d }))...)
	if err := fxApp.Err(); err != nil {
		t.Fatal(err)
	}

	nowStr := os.Getenv("WORKERBENCH_NOW")
	if nowStr == "" {
		nowStr = defaultNow
	}
	now, err := time.Parse(time.RFC3339, nowStr)
	if err != nil {
		t.Fatal(err)
	}
	util.Now = func() time.Time { return now }

	// the days a batch may rewrite: today, and yesterday when today has no elements yet
	if os.Getenv("WORKERBENCH_PRINT_DAYS") != "" {
		for _, server := range constant.Servers {
			dayNum := util.GetDayNum(&now, server)
			fmt.Printf("BENCH days server=%s from=%d to=%d\n", server, dayNum-1, dayNum)
		}
		return
	}

	ctx := context.Background()
	worker := calcwkr.NewWorkerForBench(deps)

	// the batch the long-running worker ran 30 minutes earlier
	if err := worker.RunStatsBatchForBench(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"drop_matrix_elements", "pattern_matrix_elements"} {
		if _, err := db.ExecContext(ctx, "VACUUM "+table); err != nil {
			t.Fatal(err)
		}
	}
	for name, flush := range cache.SetMap {
		if !longLivedCaches[name] {
			if err := flush(); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, flush := range cache.SingularFlusherMap {
		if !longLivedCaches[name] {
			if err := flush(); err != nil {
				t.Fatal(err)
			}
		}
	}

	// the driver samples foreign load between these markers
	fmt.Println("BENCH timed_start")
	start := time.Now()
	if err := worker.RunStatsBatchForBench(ctx); err != nil {
		t.Fatal(err)
	}
	total := time.Since(start)
	fmt.Println("BENCH timed_end")

	// per-phase durations as recorded by the worker itself
	phaseTotals := map[string]time.Duration{}
	for _, server := range constant.Servers {
		for _, phase := range phases {
			d := time.Duration(testutil.ToFloat64(observability.WorkerCalcDuration.WithLabelValues(phase, server)) * float64(time.Second))
			phaseTotals[phase] += d
			fmt.Printf("BENCH phase=%s server=%s ms=%d\n", phase, server, d.Milliseconds())
		}
	}

	// verify what the batch produced for the pinned day (the site stats are served from cache, refreshed by the batch)
	var lines []string
	for _, server := range constant.Servers {
		ss, err := siteStats.GetShimSiteStats(ctx, server)
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range ss.TotalStageTimes {
			lines = append(lines, fmt.Sprintf("ss.stage|%s|%s|%d", server, st.StageID, st.Times))
		}
		for _, st := range ss.TotalStageTimes24H {
			lines = append(lines, fmt.Sprintf("ss.stage24h|%s|%s|%d", server, st.StageID, st.Times))
		}
		for _, it := range ss.TotalItemQuantities {
			lines = append(lines, fmt.Sprintf("ss.item|%s|%s|%d", server, it.ItemID, it.Quantity))
		}
		lines = append(lines, fmt.Sprintf("ss.sanity|%s|%d", server, ss.TotalSanityCost))

		dayNum := util.GetDayNum(&now, server)
		var dmes []*model.DropMatrixElement
		if err := db.NewSelect().Model(&dmes).Where("server = ? AND day_num = ?", server, dayNum).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		for _, el := range dmes {
			lines = append(lines, dropMatrixLine(el))
		}
		var pmes []*model.PatternMatrixElement
		if err := db.NewSelect().Model(&pmes).Where("server = ? AND day_num = ?", server, dayNum).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		for _, el := range pmes {
			lines = append(lines, patternMatrixLine(el))
		}
	}

	sort.Strings(lines)
	joined := strings.Join(lines, "\n")
	sum := sha256.Sum256([]byte(joined))
	if p := os.Getenv("WORKERBENCH_DUMP"); p != "" {
		if err := os.WriteFile(p, []byte(joined+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, phase := range phases {
		fmt.Printf("BENCH phase_total=%s ms=%d\n", phase, phaseTotals[phase].Milliseconds())
	}
	fmt.Printf("BENCH lines=%d\n", len(lines))
	fmt.Printf("BENCH hash=%s\n", hex.EncodeToString(sum[:]))
	fmt.Printf("BENCH total_ms=%d\n", total.Milliseconds())
}

func dropMatrixLine(el *model.DropMatrixElement) string {
	keys := make([]int, 0, len(el.QuantityBuckets))
	for k := range el.QuantityBuckets {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%d:%d,", k, el.QuantityBuckets[k])
	}
	return fmt.Sprintf("dm|%s|%s|%d|%d|%d|%d|%d|%d|%d|%s",
		el.Server, el.SourceCategory, el.StageID, el.ItemID, el.Quantity, el.Times,
		el.StartTime.UnixMilli(), el.EndTime.UnixMilli(), el.DayNum, b.String())
}

func patternMatrixLine(el *model.PatternMatrixElement) string {
	return fmt.Sprintf("pm|%s|%s|%d|%d|%d|%d|%d|%d|%d",
		el.Server, el.SourceCategory, el.StageID, el.PatternID, el.Quantity, el.Times,
		el.StartTime.UnixMilli(), el.EndTime.UnixMilli(), el.DayNum)
}
