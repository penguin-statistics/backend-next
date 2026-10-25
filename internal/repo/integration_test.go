package repo

// Integration tests running the repository SQL against a real PostgreSQL. They are skipped unless
// PENGUIN_TEST_POSTGRES_DSN points at a disposable database, e.g.
//
//	docker run -d --rm -p 127.0.0.1:55432:5432 -e POSTGRES_PASSWORD=test postgres:14-alpine
//	PENGUIN_TEST_POSTGRES_DSN='postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable' go test ./internal/repo/
//
// Every test works in a schema of its own, which it drops afterwards.

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

// testSchema is the subset of the production schema the tested queries read and write.
const testSchema = `
CREATE TYPE server AS ENUM ('CN', 'US', 'JP', 'KR');
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
CREATE TABLE drop_matrix_elements (
	element_id bigserial PRIMARY KEY,
	stage_id integer NOT NULL,
	item_id integer NOT NULL,
	quantity integer NOT NULL,
	times integer NOT NULL,
	server server NOT NULL,
	quantity_buckets jsonb,
	source_category character varying(32) DEFAULT 'all' NOT NULL,
	start_time timestamp with time zone,
	end_time timestamp with time zone,
	day_num integer NOT NULL
);
CREATE TABLE pattern_matrix_elements (
	element_id bigserial PRIMARY KEY,
	stage_id integer NOT NULL,
	pattern_id bigint NOT NULL,
	quantity integer NOT NULL,
	times integer NOT NULL,
	server server NOT NULL,
	source_category character varying(32) DEFAULT 'all' NOT NULL,
	start_time timestamp with time zone,
	end_time timestamp with time zone,
	day_num integer NOT NULL
);
`

// dayVersionsMigration is the migration adding the day versions of the matrix elements: the tables of testSchema
// have them, as in production.
const dayVersionsMigration = "../../scripts/migrations/20261025_matrix_element_day_versions.sql"

// newTestDB connects to PENGUIN_TEST_POSTGRES_DSN with a fresh schema holding testSchema and its day versions.
func newTestDB(t *testing.T) *bun.DB {
	t.Helper()
	return newTestDBWithSchema(t, testSchema+readFile(t, dayVersionsMigration))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// newTestDBWithSchema connects to PENGUIN_TEST_POSTGRES_DSN with a fresh schema in which ddl has been run.
func newTestDBWithSchema(t *testing.T, ddl string) *bun.DB {
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
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	return db
}

func mustExec(t *testing.T, db *bun.DB, query string, args ...interface{}) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}
