//go:build workerbench

// Global matrix benchmark harness: times the calculation of the global drop and pattern matrices of every server
// and source category (what the worker's background refresh runs), with the clock pinned and the caches it reads
// warm, as they are after a batch. Prints the duration, the number of queries and a SHA-256 over the results, so
// that a faster calculation is checked to produce the same matrices. Read only.
//
// Env: WORKERBENCH_NOW (RFC3339, default 2026-10-19T14:00:00Z), GLOBALMATRIXBENCH_RUNS (default 3),
// GLOBALMATRIXBENCH_EXPLAIN (a server: print the plans of its global matrix queries, for every source category at once).
package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/app"
	"exusiai.dev/backend-next/internal/app/appcontext"
	"exusiai.dev/backend-next/internal/service"
	"exusiai.dev/backend-next/internal/util"
)

type queryCounter struct {
	n        atomic.Int64
	captured []string // the queries of the global matrices, while capture is set
	capture  bool
}

func (c *queryCounter) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

func (c *queryCounter) AfterQuery(_ context.Context, event *bun.QueryEvent) {
	c.n.Add(1)
	if c.capture && strings.HasPrefix(event.Query, "WITH t AS") {
		c.captured = append(c.captured, event.Query)
	}
}

func TestGlobalMatrixBench(t *testing.T) {
	var (
		db            *bun.DB
		dropMatrix    *service.DropMatrix
		patternMatrix *service.PatternMatrix
	)
	fxApp := fx.New(append(app.OptionsWithoutWorkers(appcontext.Declare(appcontext.EnvCLI)),
		fx.Populate(&db, &dropMatrix, &patternMatrix))...)
	if err := fxApp.Err(); err != nil {
		t.Fatal(err)
	}
	nowStr := os.Getenv("WORKERBENCH_NOW")
	if nowStr == "" {
		nowStr = "2026-10-19T14:00:00Z"
	}
	now, err := time.Parse(time.RFC3339, nowStr)
	if err != nil {
		t.Fatal(err)
	}
	util.Now = func() time.Time { return now }
	runs := 3
	if v := os.Getenv("GLOBALMATRIXBENCH_RUNS"); v != "" {
		if runs, err = strconv.Atoi(v); err != nil {
			t.Fatal(err)
		}
	}
	counter := &queryCounter{}
	db.AddQueryHook(counter)
	categories := []string{constant.SourceCategoryAll, constant.SourceCategoryManual, constant.SourceCategoryAutomated}
	ctx := context.Background()

	calculate := func() []string {
		lines := make([]string, 0)
		for _, server := range constant.Servers {
			serverStart := time.Now()
			queries := counter.n.Load()
			for _, sourceCategory := range categories {
				drop, err := dropMatrix.CalcGlobalDropMatrixForBench(ctx, server, sourceCategory)
				if err != nil {
					t.Fatal(err)
				}
				for _, el := range drop.Matrix {
					lines = append(lines, fmt.Sprintf("dm|%s|%s|%d|%d|%d|%d|%v|%d|%d", server, sourceCategory, el.StageID, el.ItemID,
						el.Times, el.Quantity, el.StdDev, el.TimeRange.StartTime.UnixMilli(), el.TimeRange.EndTime.UnixMilli()))
				}
				pattern, err := patternMatrix.CalcGlobalPatternMatrixForBench(ctx, server, sourceCategory)
				if err != nil {
					t.Fatal(err)
				}
				for _, el := range pattern.PatternMatrix {
					lines = append(lines, fmt.Sprintf("pm|%s|%s|%d|%d|%d|%d|%d|%d", server, sourceCategory, el.StageID, el.PatternID,
						el.Times, el.Quantity, el.TimeRange.StartTime.UnixMilli(), el.TimeRange.EndTime.UnixMilli()))
				}
			}
			fmt.Printf("BENCH server=%s ms=%d queries=%d\n", server, time.Since(serverStart).Milliseconds(), counter.n.Load()-queries)
		}
		sort.Strings(lines)
		return lines
	}

	if server := os.Getenv("GLOBALMATRIXBENCH_EXPLAIN"); server != "" {
		counter.capture = true
		if _, err := dropMatrix.CalcGlobalDropMatricesForBench(ctx, server, categories); err != nil {
			t.Fatal(err)
		}
		if _, err := patternMatrix.CalcGlobalPatternMatricesForBench(ctx, server, categories); err != nil {
			t.Fatal(err)
		}
		counter.capture = false
		for i, query := range counter.captured {
			if dir := os.Getenv("GLOBALMATRIXBENCH_QUERY_DIR"); dir != "" {
				if err := os.WriteFile(fmt.Sprintf("%s/query_%d.sql", dir, i), []byte(query), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			var plan []string
			if err := db.NewRaw("EXPLAIN (ANALYZE, BUFFERS) "+query).Scan(ctx, &plan); err != nil {
				t.Fatal(err)
			}
			fmt.Println("PLAN " + strings.Join(plan, "\nPLAN "))
		}
		return
	}

	// warm the caches the calculation reads (time ranges, drop infos), as a batch leaves them
	reference := calculate()
	sum := sha256.New()
	for _, line := range reference {
		fmt.Fprintln(sum, line)
	}
	if p := os.Getenv("WORKERBENCH_DUMP"); p != "" {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range reference {
			fmt.Fprintln(f, line)
		}
		f.Close()
	}
	fmt.Printf("BENCH lines=%d hash=%s\n", len(reference), hex.EncodeToString(sum.Sum(nil)))

	for run := 0; run < runs; run++ {
		start := time.Now()
		queries := counter.n.Load()
		lines := calculate()
		if len(lines) != len(reference) {
			t.Fatalf("run %d: %d lines, %d before", run, len(lines), len(reference))
		}
		fmt.Printf("BENCH run=%d total_ms=%d queries=%d\n", run, time.Since(start).Milliseconds(), counter.n.Load()-queries)
	}
}
