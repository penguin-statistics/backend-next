package v2

// An integration test of the matrix handlers on a real PostgreSQL. It is skipped unless PENGUIN_TEST_POSTGRES_DSN
// points at a disposable database, see internal/repo/integration_test.go. It works in a schema of its own, which it
// drops afterwards.

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/server/svr"
	"exusiai.dev/backend-next/internal/service"
)

// testSchema is the subset of the production schema the matrix handlers read, left empty but for one account.
const testSchema = `
CREATE TYPE server AS ENUM ('CN', 'US', 'JP', 'KR');
CREATE TYPE drop_type AS ENUM ('REGULAR', 'SPECIAL', 'EXTRA', 'FURNITURE', 'RECOGNITION_ONLY');
CREATE TABLE accounts (
	account_id serial PRIMARY KEY,
	penguin_id character varying(32) NOT NULL,
	weight real,
	created_at timestamp with time zone DEFAULT now() NOT NULL
);
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
INSERT INTO accounts (penguin_id) VALUES ('11111111');
`

// newTestDB connects to PENGUIN_TEST_POSTGRES_DSN with a fresh schema holding testSchema.
func newTestDB(t *testing.T) *bun.DB {
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
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})

	db := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(
		pgdriver.WithDSN(dsn),
		pgdriver.WithConnParams(map[string]interface{}{"search_path": schema}),
	)), pgdialect.New())
	t.Cleanup(func() { db.Close() })
	if _, err := db.ExecContext(ctx, testSchema); err != nil {
		t.Fatal(err)
	}
	return db
}

// flushCaches empties every cache, so that the handlers read the test schema.
func flushCaches(t *testing.T) {
	t.Helper()
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
}

// The personal drop and pattern matrices hold the results of the account in the Authorization header, so no shared
// cache may keep them, while the global ones are cacheable.
func TestPersonalMatricesAreNotCacheable(t *testing.T) {
	db := newTestDB(t)
	flushCaches(t)
	t.Cleanup(func() { flushCaches(t) })

	conf := &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{MatrixWorkerSourceCategories: []string{"all"}, PatternMatrixLimit: 19}}
	timeRangeService := service.NewTimeRange(repo.NewTimeRange(db), repo.NewDropInfo(db))
	dropReportService := service.NewDropReport(repo.NewDropReport(db))
	dropInfoService := service.NewDropInfo(repo.NewDropInfo(db), timeRangeService)
	stageService := service.NewStage(repo.NewStage(db))
	itemService := service.NewItem(repo.NewItem(db))
	dropMatrixService := service.NewDropMatrix(conf, timeRangeService, dropReportService, dropInfoService,
		service.NewDropMatrixElement(repo.NewDropMatrixElement(db)), stageService, itemService, nil)
	patternMatrixService := service.NewPatternMatrix(conf, timeRangeService, dropReportService, dropInfoService,
		service.NewPatternMatrixElement(repo.NewPatternMatrixElement(db)), service.NewDropPatternElement(repo.NewDropPatternElement(db)),
		stageService, itemService)
	accountService := service.NewAccount(repo.NewAccount(db))

	app := fiber.New()
	app.Use(middlewares.InjectI18n()) // for the validation of the query
	v2 := &svr.V2{Router: app.Group("/PenguinStats/api/v2")}
	RegisterResult(v2, Result{DropMatrixService: dropMatrixService, PatternMatrixService: patternMatrixService, AccountService: accountService})
	RegisterPrivate(v2, Private{DropMatrixService: dropMatrixService, PatternMatrixService: patternMatrixService, AccountService: accountService})

	notCacheable := "no-cache, no-store, must-revalidate"
	tests := []struct {
		path string
		want string
	}{
		{"/result/matrix?server=CN", "public, max-age=300"},
		{"/result/matrix?server=CN&is_personal=true", notCacheable},
		{"/result/pattern?server=CN", "public, max-age=300"},
		{"/result/pattern?server=CN&is_personal=true", notCacheable},
		{"/_private/result/matrix/CN/global", "public, max-age=300"},
		{"/_private/result/matrix/CN/personal", notCacheable},
		{"/_private/result/pattern/CN/global", "public, max-age=300"},
		{"/_private/result/pattern/CN/personal", notCacheable},
	}
	// the headers of the second request are checked, by which the global matrices have been calculated and cached
	get := func(path string) *http.Response {
		req := httptest.NewRequest(fiber.MethodGet, "/PenguinStats/api/v2"+path, nil)
		req.Header.Set(fiber.HeaderAuthorization, "PenguinID 11111111")
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
		return resp
	}
	for _, tt := range tests {
		get(tt.path)
		if got := get(tt.path).Header.Get(fiber.HeaderCacheControl); got != tt.want {
			t.Errorf("%s: Cache-Control %q, want %q", tt.path, got, tt.want)
		}
	}
}
