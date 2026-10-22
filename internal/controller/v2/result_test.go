package v2

// Integration tests running advanced queries against a real PostgreSQL. They are skipped unless
// PENGUIN_TEST_POSTGRES_DSN is set (see internal/repo/integration_test.go). Every test works in a schema of its own,
// which it drops afterwards.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	pkgerrors "github.com/pkg/errors"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/valyala/fasthttp"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/model/types"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/service"
)

// advancedQuerySchema is the subset of the production schema advanced queries read, with a stage, an item and a drop
// info of the item on the stage.
const advancedQuerySchema = `
CREATE TYPE server AS ENUM ('CN', 'US', 'JP', 'KR');
CREATE TYPE drop_type AS ENUM ('REGULAR', 'SPECIAL', 'EXTRA', 'FURNITURE', 'RECOGNITION_ONLY');
CREATE TABLE time_ranges (
	range_id bigserial PRIMARY KEY,
	name character varying(64),
	start_time timestamp with time zone NOT NULL,
	end_time timestamp with time zone NOT NULL,
	comment text,
	server server NOT NULL
);
CREATE TABLE drop_infos (
	drop_id bigserial PRIMARY KEY,
	server server NOT NULL,
	stage_id integer NOT NULL,
	item_id integer,
	drop_type drop_type NOT NULL,
	range_id bigint NOT NULL,
	bounds jsonb,
	accumulable boolean NOT NULL,
	extras jsonb
);
CREATE TABLE drop_reports (
	report_id bigserial PRIMARY KEY,
	stage_id integer NOT NULL,
	pattern_id bigint NOT NULL,
	times integer NOT NULL,
	created_at timestamp with time zone DEFAULT now() NOT NULL,
	reliability integer NOT NULL,
	server server NOT NULL,
	account_id bigint NOT NULL,
	source_name text,
	version text
);
CREATE TABLE drop_pattern_elements (
	element_id bigserial PRIMARY KEY,
	drop_pattern_id bigint NOT NULL,
	item_id integer NOT NULL,
	quantity integer NOT NULL
);
CREATE TABLE stages (
	stage_id serial PRIMARY KEY,
	ark_stage_id character varying(255) NOT NULL,
	zone_id integer NOT NULL,
	stage_type text NOT NULL,
	extra_process_type text,
	code jsonb,
	sanity integer,
	existence jsonb,
	min_clear_time integer
);
CREATE TABLE items (
	item_id serial PRIMARY KEY,
	ark_item_id character varying(255) NOT NULL,
	type text NOT NULL,
	name jsonb,
	existence jsonb,
	sort_id integer NOT NULL DEFAULT 0,
	rarity integer NOT NULL DEFAULT 0,
	"group" text,
	sprite text,
	keywords jsonb
);
INSERT INTO stages (stage_id, ark_stage_id, zone_id, stage_type) VALUES (1, 'main_01-07', 1, 'MAIN');
INSERT INTO items (item_id, ark_item_id, type) VALUES (1, '30012', 'MATERIAL');
INSERT INTO time_ranges (range_id, start_time, end_time, server) VALUES (1, '2019-01-01', '2099-01-01', 'CN');
INSERT INTO drop_infos (server, stage_id, item_id, drop_type, range_id, accumulable) VALUES ('CN', 1, 1, 'REGULAR', 1, true);
INSERT INTO drop_reports (stage_id, pattern_id, times, reliability, server, account_id, created_at) VALUES (1, 1, 1, 0, 'CN', 1, '2026-01-10 12:00:00+00');
INSERT INTO drop_pattern_elements (drop_pattern_id, item_id, quantity) VALUES (1, 1, 2);
`

// newAdvancedQueryTestResult returns a Result whose advanced queries run on a schema of their own holding
// advancedQuerySchema.
func newAdvancedQueryTestResult(t *testing.T) (*Result, *bun.DB) {
	t.Helper()
	dsn := os.Getenv("PENGUIN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PENGUIN_TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("it_%d_%d", time.Now().UnixNano(), rand.Int63())

	admin := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn))), pgdialect.New())
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer admin.Close()
		// a lookup a test gave up on can still run on the server, and deadlock with the drop: PostgreSQL then aborts one
		// of them
		for attempt := 1; ; attempt++ {
			_, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
			var pgErr pgdriver.Error
			if err == nil || attempt == 5 || !errors.As(err, &pgErr) || pgErr.Field('C') != "40P01" {
				if err != nil {
					t.Error(err)
				}
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	})

	db := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(
		pgdriver.WithDSN(dsn),
		pgdriver.WithConnParams(map[string]interface{}{"search_path": schema}),
	)), pgdialect.New())
	t.Cleanup(func() { db.Close() })
	if _, err := db.ExecContext(ctx, advancedQuerySchema); err != nil {
		t.Fatal(err)
	}

	cache.Initialize()
	for _, flush := range cache.SetMap {
		if err := flush(); err != nil {
			t.Fatal(err)
		}
	}
	for _, flush := range cache.SingularFlusherMap {
		if err := flush(); err != nil {
			t.Fatal(err)
		}
	}

	dropInfoRepo := repo.NewDropInfo(db)
	timeRange := service.NewTimeRange(repo.NewTimeRange(db), dropInfoRepo)
	dropInfo := service.NewDropInfo(dropInfoRepo, timeRange)
	dropReport := service.NewDropReport(repo.NewDropReport(db), nil)
	dropMatrixElement := service.NewDropMatrixElement(repo.NewDropMatrixElement(db))
	stage := service.NewStage(repo.NewStage(db))
	item := service.NewItem(repo.NewItem(db))
	return &Result{
		DropMatrixService: &service.DropMatrix{
			Config: &appconfig.Config{}, TimeRangeService: timeRange, DropReportService: dropReport, DropInfoService: dropInfo,
			DropMatrixElementService: dropMatrixElement, StageService: stage, ItemService: item,
		},
		DropReportService: dropReport,
		TrendService:      service.NewTrend(dropReport, dropInfo, stage, item, dropMatrixElement),
		ItemService:       item,
		StageService:      stage,
	}, db
}

// lockTable holds a lock on table for 5 seconds, or until the test ends, so that every query reading it waits for as
// long.
func lockTable(t *testing.T, db *bun.DB, table string) {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	locker, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = locker.Rollback() })
	time.AfterFunc(5*time.Second, func() { _ = locker.Rollback() })
	if _, err := locker.ExecContext(ctx, "LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
}

var advancedQueryTestQueries = map[string]*types.AdvancedQuery{
	"matrix": {Server: "CN", StageID: "main_01-07", ItemIDs: []string{"30012"}},
	"trend": {
		Server: "CN", StageID: "main_01-07", ItemIDs: []string{"30012"},
		StartTime: time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC).UnixMilli(), EndTime: time.Date(2026, 1, 11, 0, 0, 0, 0, time.UTC).UnixMilli(),
		Interval: null.IntFrom(24 * time.Hour.Milliseconds()),
	},
}

func TestRunAdvancedQueryReturnsResults(t *testing.T) {
	c, _ := newAdvancedQueryTestResult(t)
	// the report of advancedQuerySchema
	want := map[string]string{
		"matrix": `{"stageId":"main_01-07","itemId":"30012","times":1,"quantity":2,`,
		"trend":  `{"30012":{"quantity":[0,2],"times":[0,1]}}`,
	}
	app := fiber.New()
	for name, query := range advancedQueryTestQueries {
		t.Run(name, func(t *testing.T) {
			ctx := app.AcquireCtx(&fasthttp.RequestCtx{})
			defer app.ReleaseCtx(ctx)
			result, err := c.runAdvancedQuery(ctx, query, time.Now().Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), want[name]) {
				t.Errorf("result %s does not contain %s", b, want[name])
			}
		})
	}
}

func TestRunAdvancedQueryCancelsStatementsAtDeadline(t *testing.T) {
	c, db := newAdvancedQueryTestResult(t)
	app := fiber.New()
	for name, query := range advancedQueryTestQueries {
		t.Run(name, func(t *testing.T) {
			lockTable(t, db, "drop_reports")
			ctx := app.AcquireCtx(&fasthttp.RequestCtx{})
			defer app.ReleaseCtx(ctx)

			// the aggregations wait 5 seconds for the lock, unless PostgreSQL cancels them at the deadline
			begin := time.Now()
			_, err := c.runAdvancedQuery(ctx, query, time.Now().Add(500*time.Millisecond))
			if err != ErrAdvancedQueryTimeout {
				t.Fatalf("got error %v, want ErrAdvancedQueryTimeout", err)
			}
			if elapsed := time.Since(begin); elapsed > 2500*time.Millisecond {
				t.Fatalf("query was cancelled after %v", elapsed)
			}
		})
	}
}

func TestRunAdvancedQueryAfterDeadline(t *testing.T) {
	c, db := newAdvancedQueryTestResult(t)
	lockTable(t, db, "drop_reports")
	app := fiber.New()
	ctx := app.AcquireCtx(&fasthttp.RequestCtx{})
	defer app.ReleaseCtx(ctx)

	begin := time.Now()
	_, err := c.runAdvancedQuery(ctx, advancedQueryTestQueries["matrix"], time.Now())
	if err != ErrAdvancedQueryTimeout {
		t.Fatalf("got error %v, want ErrAdvancedQueryTimeout", err)
	}
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Fatalf("query ran for %v", elapsed)
	}
}

func TestRunAdvancedQueryBoundsLookups(t *testing.T) {
	app := fiber.New()
	// the stage is looked up before the aggregations, the drop infos inside the matrix and the trend queries
	for _, table := range []string{"stages", "drop_infos"} {
		for name, query := range advancedQueryTestQueries {
			t.Run(table+"/"+name, func(t *testing.T) {
				c, db := newAdvancedQueryTestResult(t)
				lockTable(t, db, table)
				ctx := app.AcquireCtx(&fasthttp.RequestCtx{})
				defer app.ReleaseCtx(ctx)

				// the lookup waits 5 seconds for the lock, unless its context ends advancedQueryLookupGrace after the
				// deadline
				begin := time.Now()
				_, err := c.runAdvancedQuery(ctx, query, time.Now().Add(100*time.Millisecond))
				if err != ErrAdvancedQueryTimeout {
					t.Fatalf("got error %v, want ErrAdvancedQueryTimeout", err)
				}
				if elapsed := time.Since(begin); elapsed > advancedQueryLookupGrace+time.Second {
					t.Fatalf("query ran for %v", elapsed)
				}
			})
		}
	}
}

func TestRunAdvancedQueryOnOneConnection(t *testing.T) {
	c, db := newAdvancedQueryTestResult(t)
	// a query holding the connection across its lookups would make them wait for it until the context ends
	db.SetMaxOpenConns(1)
	app := fiber.New()
	for name, query := range advancedQueryTestQueries {
		t.Run(name, func(t *testing.T) {
			ctx := app.AcquireCtx(&fasthttp.RequestCtx{})
			defer app.ReleaseCtx(ctx)
			if _, err := c.runAdvancedQuery(ctx, query, time.Now().Add(5*time.Second)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunAdvancedQueryAfterATimedOutStageListLookup(t *testing.T) {
	app := fiber.New()
	for name, query := range advancedQueryTestQueries {
		t.Run(name, func(t *testing.T) {
			c, db := newAdvancedQueryTestResult(t)
			ctx := app.AcquireCtx(&fasthttp.RequestCtx{})
			defer app.ReleaseCtx(ctx)
			// with the stage of the query cached, the first query reading stages is the stage list lookup of the result
			// conversion, after the aggregations
			if _, err := c.StageService.GetStageByArkId(context.Background(), query.StageID); err != nil {
				t.Fatal(err)
			}
			lockTable(t, db, "stages")

			if _, err := c.runAdvancedQuery(ctx, query, time.Now().Add(100*time.Millisecond)); err != ErrAdvancedQueryTimeout {
				t.Fatalf("got error %v, want ErrAdvancedQueryTimeout", err)
			}
			// the failed lookup must not leave an empty stage list in the cache: the next query reads the stages once
			// the lock is released
			if _, err := c.runAdvancedQuery(ctx, query, time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAdvancedQueryErrorMapsStatementTimeout(t *testing.T) {
	_, db := newAdvancedQueryTestResult(t)
	ctx := context.Background()

	timeoutErr := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SET LOCAL statement_timeout = 100"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "SELECT pg_sleep(5)")
		return err
	})
	if timeoutErr == nil {
		t.Fatal("pg_sleep was not cancelled")
	}
	if got := advancedQueryError(timeoutErr); got != ErrAdvancedQueryTimeout {
		t.Errorf("advancedQueryError(%v) = %v, want ErrAdvancedQueryTimeout", timeoutErr, got)
	}
	if got := advancedQueryError(pkgerrors.Wrap(timeoutErr, "calc")); got != ErrAdvancedQueryTimeout {
		t.Errorf("advancedQueryError of a wrapped statement timeout = %v, want ErrAdvancedQueryTimeout", got)
	}

	// a statement cancelled by pg_cancel_backend has the SQLSTATE of a statement timeout too
	marker := fmt.Sprintf("cancel test %d", time.Now().UnixNano())
	go func() {
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			var cancelled int
			if err := db.NewRaw("SELECT count(pg_cancel_backend(pid)) FROM pg_stat_activity WHERE query LIKE ? AND pid <> pg_backend_pid()", "%"+marker).Scan(ctx, &cancelled); err != nil || cancelled > 0 {
				return
			}
		}
	}()
	_, cancelErr := db.ExecContext(ctx, "SELECT pg_sleep(5) -- "+marker)
	var pgErr pgdriver.Error
	if !errors.As(cancelErr, &pgErr) || !pgErr.StatementTimeout() {
		t.Fatalf("got error %v, want a statement cancelled with SQLSTATE 57014", cancelErr)
	}

	// any other error, from the database or not, is returned as it is
	_, otherErr := db.ExecContext(ctx, "SELECT * FROM table_that_does_not_exist")
	if otherErr == nil {
		t.Fatal("query of a missing table succeeded")
	}
	for _, err := range []error{cancelErr, otherErr, fmt.Errorf("not a database error"), nil} {
		if got := advancedQueryError(err); !reflect.DeepEqual(got, err) {
			t.Errorf("advancedQueryError(%v) = %v, want it unchanged", err, got)
		}
	}
}

// the global drop matrices are served from the cache, which the tests fill instead of a database
func TestGetDropMatrixLastModifiedOfCategory(t *testing.T) {
	cache.Initialize()
	t.Cleanup(func() {
		cache.ShimGlobalDropMatrix.Flush()
		cache.LastModifiedTime.Flush()
	})
	lastModified := map[string]time.Time{
		constant.SourceCategoryAll:    time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		constant.SourceCategoryManual: time.Date(2026, 10, 20, 13, 0, 0, 0, time.UTC),
	}
	for category, modified := range lastModified {
		key := "CN" + constant.CacheSep + "false" + constant.CacheSep + category
		cache.ShimGlobalDropMatrix.Set(key, modelv2.DropMatrixQueryResult{Matrix: []*modelv2.OneDropMatrixElement{{StageID: category}}}, time.Hour)
		cache.LastModifiedTime.Set("[shimGlobalDropMatrix#server|showClosedZones|sourceCategory:"+key+"]", modified, 0)
	}

	app := fiber.New()
	app.Use(middlewares.InjectI18n()) // for the validation of the query
	c := &Result{DropMatrixService: &service.DropMatrix{}}
	app.Get("/result/matrix", c.GetDropMatrix)
	for category, modified := range lastModified {
		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/result/matrix?server=CN&category="+category, nil), -1)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("%s: status %d", category, resp.StatusCode)
		}
		if got, want := resp.Header.Get(fiber.HeaderLastModified), modified.Format(http.TimeFormat); got != want {
			t.Errorf("%s: Last-Modified %q, want %q", category, got, want)
		}
	}
}
