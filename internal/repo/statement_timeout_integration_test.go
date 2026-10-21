package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/driver/pgdriver"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
)

// assertStatementTimeout fails unless err is PostgreSQL cancelling a statement for exceeding statement_timeout, and it
// came well before a statement taking wouldTake would have finished by itself.
func assertStatementTimeout(t *testing.T, err error, elapsed, wouldTake time.Duration) {
	t.Helper()
	var pgErr pgdriver.Error
	if !errors.As(err, &pgErr) || !pgErr.StatementTimeout() {
		t.Fatalf("got error %v, want a statement timeout", err)
	}
	if elapsed > wouldTake/2 {
		t.Fatalf("statement was cancelled after %v", elapsed)
	}
}

// countRows runs a count(*) query on a connection of its own.
func countRows(t *testing.T, db *bun.DB, query string, args ...interface{}) int {
	t.Helper()
	var n int
	if err := db.NewRaw(query, args...).Scan(context.Background(), &n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRunWithStatementTimeoutCancelsSleepOnServer(t *testing.T) {
	db := newTestDB(t)
	repo := NewDropReport(db)
	ctx := context.Background()

	// a timeout of 0 is no timeout for PostgreSQL, but not for RunWithStatementTimeout
	for _, timeout := range []time.Duration{200 * time.Millisecond, 0} {
		marker := fmt.Sprintf("statement timeout test %d", time.Now().UnixNano())

		// the client never cancels: only the server can end the statement before its 5 seconds are up
		begin := time.Now()
		err := repo.RunWithStatementTimeout(ctx, timeout, func(ctx context.Context) error {
			return repo.runWithStatementDeadline(ctx, func(ctx context.Context, db bun.IConn) error {
				_, err := db.ExecContext(ctx, "SELECT pg_sleep(5) -- "+marker)
				return err
			})
		})
		assertStatementTimeout(t, err, time.Since(begin), 5*time.Second)

		if n := countRows(t, db, "SELECT count(*) FROM pg_stat_activity WHERE query LIKE ? AND pid <> pg_backend_pid()", "%"+marker); n != 0 {
			t.Fatalf("%d backends still run the statement", n)
		}
	}
}

func TestRunWithStatementTimeoutIsADeadline(t *testing.T) {
	db := newTestDB(t)
	repo := NewDropReport(db)

	// the statement starts 800 ms into the timeout of 1 s: PostgreSQL cancels it after what is left, not after 1 s
	begin := time.Now()
	err := repo.RunWithStatementTimeout(context.Background(), time.Second, func(ctx context.Context) error {
		time.Sleep(800 * time.Millisecond)
		return repo.runWithStatementDeadline(ctx, func(ctx context.Context, db bun.IConn) error {
			_, err := db.ExecContext(ctx, "SELECT pg_sleep(5)")
			return err
		})
	})
	elapsed := time.Since(begin)
	assertStatementTimeout(t, err, elapsed, 5*time.Second)
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("statement was cancelled %v after the timeout began, want about 1s", elapsed)
	}
}

func TestRunWithStatementTimeoutCancelsAggregationsOnServer(t *testing.T) {
	db := newTestDB(t)
	repo := NewDropReport(db)
	mustExec(t, db, `INSERT INTO drop_reports (stage_id, pattern_id, times, reliability, server, account_id) VALUES (1, 1, 1, 0, 'CN', 1)`)

	start, end := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	queryCtx := &model.DropReportQueryContext{
		Server:          "CN",
		StartTime:       &start,
		EndTime:         &end,
		StageItemFilter: &map[int][]int{1: {11}},
		SourceCategory:  constant.SourceCategoryAll,
	}
	aggregations := map[string]func(ctx context.Context) error{
		"CalcTotalTimes": func(ctx context.Context) error {
			_, err := repo.CalcTotalTimes(ctx, queryCtx)
			return err
		},
		"CalcQuantityUniqCount": func(ctx context.Context) error {
			_, err := repo.CalcQuantityUniqCount(ctx, queryCtx)
			return err
		},
		"CalcDropMatrixForIntervalsBySourceCategories": func(ctx context.Context) error {
			_, _, err := repo.CalcDropMatrixForIntervalsBySourceCategories(ctx, []*model.DropReportQueryContext{queryCtx}, []string{constant.SourceCategoryAll})
			return err
		},
		"CalcTotalQuantityForTrend": func(ctx context.Context) error {
			_, err := repo.CalcTotalQuantityForTrend(ctx, "CN", &start, time.Hour, 2, map[int][]int{1: {11}}, null.Int{}, constant.SourceCategoryAll)
			return err
		},
		"CalcTotalTimesForTrend": func(ctx context.Context) error {
			_, err := repo.CalcTotalTimesForTrend(ctx, "CN", &start, time.Hour, 2, []int{1}, null.Int{}, constant.SourceCategoryAll)
			return err
		},
	}
	for name, aggregate := range aggregations {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()

			// hold a lock on drop_reports for 5 seconds, so that the aggregation waits for as long
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			locker, err := conn.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer locker.Rollback()
			time.AfterFunc(5*time.Second, func() { _ = locker.Rollback() })
			if _, err := locker.ExecContext(ctx, "LOCK TABLE drop_reports IN ACCESS EXCLUSIVE MODE"); err != nil {
				t.Fatal(err)
			}

			begin := time.Now()
			err = repo.RunWithStatementTimeout(ctx, 500*time.Millisecond, aggregate)
			assertStatementTimeout(t, err, time.Since(begin), 5*time.Second)

			// nothing waits for the lock any more
			if n := countRows(t, db, "SELECT count(*) FROM pg_locks WHERE relation = 'drop_reports'::regclass AND NOT granted"); n != 0 {
				t.Fatalf("%d lock requests on drop_reports are still waiting", n)
			}
		})
	}
}

// sortedJSON returns the JSON encodings of the elements of results, sorted.
func sortedJSON[T any](t *testing.T, results []T) []string {
	t.Helper()
	encoded := make([]string, 0, len(results))
	for _, result := range results {
		b, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		encoded = append(encoded, string(b))
	}
	sort.Strings(encoded)
	return encoded
}

func TestRunWithStatementTimeoutKeepsResultsAndSessions(t *testing.T) {
	db := newTestDB(t)
	// a single connection: the statements after the transactions run on the session they used, and a query fn runs
	// between the aggregations waits for that connection until ctx ends if an aggregation still holds it
	db.SetMaxOpenConns(1)
	repo := NewDropReport(db)
	f := seedDropReports(t, rand.New(rand.NewSource(3)), db)
	// started after the seeding, which takes long on a busy database
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	end := f.boundaries[len(f.boundaries)-1]
	stageItemFilter := map[int][]int{1: {11, 12}, 2: {}, 3: {13}}
	queryCtx := &model.DropReportQueryContext{
		Server:          "CN",
		StartTime:       &f.base,
		EndTime:         &end,
		AccountID:       null.Int{},
		StageItemFilter: &stageItemFilter,
		SourceCategory:  constant.SourceCategoryManual,
	}
	aggregate := func(ctx context.Context) ([][]string, error) {
		times, err := repo.CalcTotalTimes(ctx, queryCtx)
		if err != nil {
			return nil, err
		}
		quantities, err := repo.CalcQuantityUniqCount(ctx, queryCtx)
		if err != nil {
			return nil, err
		}
		trendQuantities, err := repo.CalcTotalQuantityForTrend(ctx, "CN", &f.base, 3*time.Hour, 8, stageItemFilter, null.Int{}, constant.SourceCategoryAll)
		if err != nil {
			return nil, err
		}
		trendTimes, err := repo.CalcTotalTimesForTrend(ctx, "CN", &f.base, 3*time.Hour, 8, f.stageIds, null.IntFrom(1), constant.SourceCategoryAll)
		if err != nil {
			return nil, err
		}
		return [][]string{sortedJSON(t, times), sortedJSON(t, quantities), sortedJSON(t, trendQuantities), sortedJSON(t, trendTimes)}, nil
	}

	var timeoutBefore string
	if err := db.NewRaw("SHOW statement_timeout").Scan(ctx, &timeoutBefore); err != nil {
		t.Fatal(err)
	}
	want, err := aggregate(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string
	var timeoutInside int
	err = repo.RunWithStatementTimeout(ctx, time.Minute, func(ctx context.Context) error {
		if err := repo.runWithStatementDeadline(ctx, func(ctx context.Context, db bun.IConn) error {
			return db.QueryRowContext(ctx, "SELECT setting::int FROM pg_settings WHERE name = 'statement_timeout'").Scan(&timeoutInside)
		}); err != nil {
			return err
		}
		got, err = aggregate(ctx)
		if err != nil {
			return err
		}
		// a lookup on the pool, as the advanced queries run between their aggregations
		var one int
		return db.NewRaw("SELECT 1").Scan(ctx, &one)
	})
	if err != nil {
		t.Fatal(err)
	}
	if timeoutInside <= 0 || timeoutInside > int(time.Minute.Milliseconds()) {
		t.Errorf("statement_timeout in the transaction is %d ms, want the time left of 1 minute", timeoutInside)
	}
	for i := range want {
		if len(want[i]) == 0 {
			t.Errorf("aggregation %d has no results", i)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("results in the transaction differ\ngot  %v\nwant %v", got, want)
	}

	var timeoutAfter string
	if err := db.NewRaw("SHOW statement_timeout").Scan(ctx, &timeoutAfter); err != nil {
		t.Fatal(err)
	}
	if timeoutAfter != timeoutBefore {
		t.Errorf("statement_timeout of the session is %q after the transaction, want %q", timeoutAfter, timeoutBefore)
	}
}
