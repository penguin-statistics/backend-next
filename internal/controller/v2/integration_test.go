package v2

// Integration tests running the handlers with their services on a real PostgreSQL. They are skipped unless
// PENGUIN_TEST_POSTGRES_DSN points at a disposable database, see internal/repo/integration_test.go. Every test works
// in a schema of its own, which it drops afterwards.

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/service"
)

// testSchema is the subset of the production schema the tested handlers read, with one account (without reports).
// server is text rather than the server enum, so that each test can use a server of its own: the last modified times
// recorded for a server outlive the test (and its caches), and would be those of another test running for the same
// server.
const testSchema = `
CREATE TABLE zones (
	zone_id serial PRIMARY KEY,
	ark_zone_id character varying(255) NOT NULL,
	index integer NOT NULL DEFAULT 0,
	category text NOT NULL,
	type text,
	name jsonb,
	existence jsonb,
	background text
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
CREATE TABLE time_ranges (
	range_id bigserial PRIMARY KEY,
	name character varying(64),
	start_time timestamp with time zone NOT NULL,
	end_time timestamp with time zone NOT NULL,
	comment text,
	server text NOT NULL
);
CREATE TABLE drop_infos (
	drop_id bigserial PRIMARY KEY,
	server text NOT NULL,
	stage_id integer NOT NULL,
	item_id integer,
	drop_type text NOT NULL,
	range_id bigint NOT NULL,
	bounds jsonb,
	accumulable boolean NOT NULL,
	extras jsonb
);
CREATE TABLE accounts (
	account_id serial PRIMARY KEY,
	penguin_id character varying(32) NOT NULL,
	weight real,
	created_at timestamp with time zone DEFAULT now() NOT NULL
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

func mustExec(t *testing.T, db *bun.DB, query string, args ...interface{}) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

// testServer returns a server no other test (or run of it) uses, for which no last modified time is recorded yet.
func testServer() string {
	return fmt.Sprintf("T%d", time.Now().UnixNano())
}

// GetStages reads the last modified time of the stages before the stages: if they are recalculated changed during the
// request, the response looks older than it is (and is sent in full again next time), rather than an older response
// having the time of newer stages
func TestGetStagesLastModifiedRecalculated(t *testing.T) {
	db := newTestDB(t)
	server := testServer()
	mustExec(t, db, `INSERT INTO zones (ark_zone_id, category, name, existence) VALUES ('main_1', 'MAINLINE', '{"en": "1"}', '{}')`)
	mustExec(t, db, `INSERT INTO stages (ark_stage_id, zone_id, stage_type, code, existence) VALUES ('main_01-07', 1, 'MAIN', '{"en": "1-7"}', '{}')`)
	app := newCacheTestApp(t)
	c := &Stage{StageService: service.NewStage(repo.NewStage(db))}
	app.Get("/stages", c.GetStages)
	target := "/stages?server=" + server
	t.Cleanup(func() {
		_ = cache.ShimStages.Delete(server)
	})

	// calculated during the first request, whose response is sent without a last modified time
	get(t, app, target, "")
	resp, body := getCacheable(t, app, target)
	lastModified := resp.Header.Get(fiber.HeaderLastModified)
	if resp.StatusCode != fiber.StatusOK || !strings.Contains(body, `"1-7"`) || lastModified == "" {
		t.Fatalf("status %d, Last-Modified %q, body %s", resp.StatusCode, lastModified, body)
	}

	// the stages expire and are recalculated unchanged: the controller reads the time the service records
	if err := cache.ShimStages.Delete(server); err != nil {
		t.Fatal(err)
	}
	if resp, _ = get(t, app, target, ""); resp.Header.Get(fiber.HeaderLastModified) != lastModified {
		t.Errorf("after an unchanged recalculation: Last-Modified %q, want %q", resp.Header.Get(fiber.HeaderLastModified), lastModified)
	}

	// the stages change and are recalculated during the request
	mustExec(t, db, `UPDATE stages SET code = '{"en": "1-8"}'`)
	if err := cache.ShimStages.Delete(server); err != nil {
		t.Fatal(err)
	}
	resp, body = get(t, app, target, "")
	if resp.StatusCode != fiber.StatusOK || !strings.Contains(body, `"1-8"`) {
		t.Fatalf("after a change: status %d, body %s; want 200 with the new stages", resp.StatusCode, body)
	}
	if got := resp.Header.Get(fiber.HeaderLastModified); got != lastModified {
		t.Errorf("the response recalculated during the request has Last-Modified %q, want the one read before, %q", got, lastModified)
	}
}

// a conditional GetStages whose stages are recalculated changed during the request is answered with the new stages, not
// 304: the last modified time read before them is the one of the client's copy
func TestGetStagesNotModifiedRecalculated(t *testing.T) {
	db := newTestDB(t)
	server := testServer()
	mustExec(t, db, `INSERT INTO zones (ark_zone_id, category, name, existence) VALUES ('main_1', 'MAINLINE', '{"en": "1"}', '{}')`)
	mustExec(t, db, `INSERT INTO stages (ark_stage_id, zone_id, stage_type, code, existence) VALUES ('main_01-07', 1, 'MAIN', '{"en": "1-7"}', '{}')`)
	app := newCacheTestApp(t)
	c := &Stage{StageService: service.NewStage(repo.NewStage(db))}
	app.Get("/stages", c.GetStages)
	target := "/stages?server=" + server
	t.Cleanup(func() {
		_ = cache.ShimStages.Delete(server)
	})

	get(t, app, target, "") // calculated, sent without a last modified time
	resp, _ := get(t, app, target, "")
	lastModified := resp.Header.Get(fiber.HeaderLastModified)
	if lastModified == "" {
		t.Fatalf("status %d, no Last-Modified", resp.StatusCode)
	}

	// the stages expire unchanged: the conditional request recalculating them is answered with 304
	if err := cache.ShimStages.Delete(server); err != nil {
		t.Fatal(err)
	}
	if resp, body := get(t, app, target, lastModified); resp.StatusCode != fiber.StatusNotModified {
		t.Fatalf("after an unchanged recalculation: status %d, body %s; want 304", resp.StatusCode, body)
	}

	// the stages change and expire: the conditional request recalculating them gets them
	mustExec(t, db, `UPDATE stages SET code = '{"en": "1-8"}'`)
	if err := cache.ShimStages.Delete(server); err != nil {
		t.Fatal(err)
	}
	resp, body := get(t, app, target, lastModified)
	if resp.StatusCode != fiber.StatusOK || !strings.Contains(body, `"1-8"`) {
		t.Fatalf("after a change: status %d, body %s; want 200 with the new stages", resp.StatusCode, body)
	}
	if got := resp.Header.Get(fiber.HeaderLastModified); got != lastModified {
		t.Errorf("Last-Modified %q, want the one read before the recalculation, %q", got, lastModified)
	}
	// and the next one is answered with the new time
	resp, _ = get(t, app, target, lastModified)
	if resp.StatusCode != fiber.StatusOK || resp.Header.Get(fiber.HeaderLastModified) == lastModified {
		t.Errorf("next request: status %d, Last-Modified %q; want 200 with a newer time", resp.StatusCode, resp.Header.Get(fiber.HeaderLastModified))
	}
}

// personal and filtered drop matrices are sent without Last-Modified, so never answered with 304, even when the global
// one they are computed alongside is not modified: the client's copy is of other data. Personal ones are marked as
// not cacheable, filtered ones carry no caching headers.
func TestGetDropMatrixNotCacheable(t *testing.T) {
	db := newTestDB(t)
	app := newCacheTestApp(t)
	t.Cleanup(func() {
		for _, flush := range cache.SetMap {
			_ = flush()
		}
		for _, flush := range cache.SingularFlusherMap {
			_ = flush()
		}
	})
	timeRange := service.NewTimeRange(repo.NewTimeRange(db), repo.NewDropInfo(db))
	dropMatrix := service.NewDropMatrix(&appconfig.Config{}, timeRange, nil, service.NewDropInfo(repo.NewDropInfo(db), timeRange), nil,
		service.NewStage(repo.NewStage(db)), service.NewItem(repo.NewItem(db)), nil)
	account := service.NewAccount(nil)
	result := &Result{DropMatrixService: dropMatrix, AccountService: account}
	private := &Private{DropMatrixService: dropMatrix, AccountService: account}
	app.Get("/result/matrix", result.GetDropMatrix)
	app.Get("/_private/result/matrix/:server/:source/:category?", private.GetDropMatrix)

	// the global drop matrix (with closed zones), last modified a minute ago, from which filtered ones are computed
	cache.GlobalDropMatrix.Set("CN"+constant.CacheSep+constant.SourceCategoryAll, model.DropMatrixQueryResult{Matrix: []*model.OneDropMatrixElement{}}, time.Hour)
	key := "CN" + constant.CacheSep + "true" + constant.CacheSep + constant.SourceCategoryAll
	cache.ShimGlobalDropMatrix.Set(key, modelv2.DropMatrixQueryResult{Matrix: []*modelv2.OneDropMatrixElement{}}, time.Hour)
	cache.LastModifiedTime.Set("[shimGlobalDropMatrix#server|showClosedZones|sourceCategory:"+key+"]", time.Now().Add(-time.Minute), 0)
	// an account without reports
	penguinId := "T" + testServer()
	cache.AccountByPenguinID.Set(penguinId, model.Account{AccountID: 1, PenguinID: penguinId}, time.Hour)

	tests := []struct {
		name         string
		target       string
		cacheable    bool
		cacheControl string // of the 200 of a matrix that is not cacheable
	}{
		{"global", "/result/matrix?server=CN&show_closed_zones=true", true, ""},
		{"global (private)", "/_private/result/matrix/CN/global", true, ""},
		{"personal", "/result/matrix?server=CN&show_closed_zones=true&is_personal=true", false, "no-cache, no-store, must-revalidate"},
		{"personal (private)", "/_private/result/matrix/CN/personal", false, "no-cache, no-store, must-revalidate"},
		{"filtered", "/result/matrix?server=CN&show_closed_zones=true&stageFilter=main_01-07", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(fiber.MethodGet, tt.target, nil)
			req.Header.Set(fiber.HeaderIfModifiedSince, time.Now().UTC().Format(http.TimeFormat))
			req.Header.Set(fiber.HeaderAuthorization, constant.PenguinIDAuthorizationRealm+" "+penguinId)
			resp, err := app.Test(req, -1)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}

			if tt.cacheable {
				if resp.StatusCode != fiber.StatusNotModified {
					t.Fatalf("status %d, body %s; want 304", resp.StatusCode, body)
				}
				return
			}
			if resp.StatusCode != fiber.StatusOK || !strings.Contains(string(body), `"matrix"`) {
				t.Fatalf("status %d, body %s; want 200 with the matrix", resp.StatusCode, body)
			}
			if got := resp.Header.Get(fiber.HeaderLastModified); got != "" {
				t.Errorf("Last-Modified %q, want none", got)
			}
			if got := resp.Header.Get(fiber.HeaderCacheControl); got != tt.cacheControl {
				t.Errorf("Cache-Control %q, want %q", got, tt.cacheControl)
			}
		})
	}
}
