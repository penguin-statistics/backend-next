package duckdbx

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func open(t *testing.T, cfg Config) *sql.DB {
	t.Helper()
	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func queryString(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var s string
	if err := db.QueryRow(query, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return s
}

func TestOpenAppliesConfig(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "tmp")
	db := open(t, Config{
		Path:          filepath.Join(dir, "job.duckdb"),
		Threads:       2,
		MemoryLimit:   "256MB",
		TempDirectory: tmp,
	})

	// the cold storage files and queries are validated against this exact DuckDB release
	if v := queryString(t, db, "SELECT version()"); v != "v1.5.6" {
		t.Errorf("version() = %q, want v1.5.6", v)
	}
	want := map[string]string{
		"threads":                      "2",
		"temp_directory":               tmp,
		"autoinstall_known_extensions": "false",
		"autoload_known_extensions":    "false",
	}
	for name, value := range want {
		if got := queryString(t, db, "SELECT current_setting($1)::VARCHAR", name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	// DuckDB reports the limit in its own units (256MB -> 244.1 MiB)
	if got := queryString(t, db, "SELECT current_setting('memory_limit')"); !strings.HasPrefix(got, "244") {
		t.Errorf("memory_limit = %q, want 256MB", got)
	}
}

func TestOpenRejectsBadConfig(t *testing.T) {
	for _, cfg := range []Config{{Path: "a?b"}, {Threads: -1}} {
		if db, err := Open(cfg); err == nil {
			_ = db.Close()
			t.Errorf("Open(%+v) succeeded, want error", cfg)
		}
	}
}

func TestParquetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	db := open(t, Config{Threads: 2, MemoryLimit: "256MB", TempDirectory: filepath.Join(dir, "tmp")})
	file := filepath.Join(dir, "drop_reports_2026-01.parquet")

	if _, err := db.Exec(`CREATE TABLE t AS
		SELECT i::BIGINT AS report_id, (i % 7)::INTEGER AS stage_id, 'CN' AS server,
			TIMESTAMPTZ '2026-01-01 00:00:00+00' + INTERVAL (i) SECOND AS created_at
		FROM range(10000) r(i)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(`COPY (SELECT * FROM t ORDER BY server, created_at) TO '` + file + `' (FORMAT parquet,
		PARQUET_VERSION V2, COMPRESSION zstd, ROW_GROUP_SIZE 122880,
		KV_METADATA {sort: 'server,created_at', month: '2026-01'})`); err != nil {
		t.Fatalf("copy: %v", err)
	}

	// the full-column content hash must survive the round trip, not just the row count
	hash := `SELECT count(*)::VARCHAR || ':' || sum(hash(report_id, stage_id, server, created_at))::VARCHAR FROM `
	src := queryString(t, db, hash+"t")
	got := queryString(t, db, hash+"read_parquet(['"+file+"'])")
	if got != src {
		t.Errorf("parquet content = %s, want %s", got, src)
	}

	if c := queryString(t, db, "SELECT DISTINCT compression FROM parquet_metadata(['"+file+"'])"); c != "ZSTD" {
		t.Errorf("compression = %q, want ZSTD", c)
	}
	// V2 data pages use the DELTA encodings for integers
	if e := queryString(t, db, "SELECT encodings FROM parquet_metadata(['"+file+"']) WHERE path_in_schema = 'report_id'"); !strings.Contains(e, "DELTA") {
		t.Errorf("report_id encodings = %q, want a V2 DELTA encoding", e)
	}
	if m := queryString(t, db, "SELECT decode(value) FROM parquet_kv_metadata(['"+file+"']) WHERE decode(key) = 'month'"); m != "2026-01" {
		t.Errorf("kv metadata month = %q, want 2026-01", m)
	}
}

func TestLoadNeverInstalls(t *testing.T) {
	// with an empty extension directory, LOAD must fail instead of downloading the extension
	db := open(t, Config{ExtensionDirectory: t.TempDir()})
	if _, err := db.Exec("LOAD postgres"); err == nil {
		t.Fatal("LOAD postgres succeeded from an empty extension directory")
	}
}

// TestLoadPostgres checks a preinstalled postgres extension, e.g. the one in the runtime image:
//
//	PENGUIN_TEST_DUCKDB_EXTENSION_DIR=/opt/duckdb/extensions [PENGUIN_TEST_POSTGRES_DSN=...] go test ./internal/pkg/duckdbx
func TestLoadPostgres(t *testing.T) {
	extDir := os.Getenv("PENGUIN_TEST_DUCKDB_EXTENSION_DIR")
	if extDir == "" {
		t.Skip("PENGUIN_TEST_DUCKDB_EXTENSION_DIR is not set")
	}
	db := open(t, Config{ExtensionDirectory: extDir})
	if _, err := db.Exec("LOAD postgres"); err != nil {
		t.Fatalf("LOAD postgres: %v", err)
	}

	dsn := os.Getenv("PENGUIN_TEST_POSTGRES_DSN")
	if dsn == "" {
		return
	}
	if _, err := db.Exec("ATTACH '" + strings.ReplaceAll(dsn, "'", "''") + "' AS pg (TYPE postgres, READ_ONLY)"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if got := queryString(t, db, "SELECT x::VARCHAR FROM postgres_query('pg', 'SELECT 42 AS x')"); got != "42" {
		t.Errorf("postgres_query = %q, want 42", got)
	}
}

func TestAllowedDirectoriesLockTheDatabaseDown(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "coldstore")
	tmp := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(allowed, 0o755); err != nil {
		t.Fatal(err)
	}
	// written before the lockdown, by another database
	w := open(t, Config{})
	inside, outside := filepath.Join(allowed, "x.parquet"), filepath.Join(dir, "coldstore-other.parquet")
	for _, f := range []string{inside, outside} {
		if _, err := w.Exec(`COPY (SELECT 42 AS x) TO '` + f + `' (FORMAT parquet)`); err != nil {
			t.Fatal(err)
		}
	}

	db := open(t, Config{
		Threads:              2,
		MemoryLimit:          "64MB",
		TempDirectory:        tmp,
		TimeZone:             "UTC",
		ParquetMetadataCache: true,
		AllowedDirectories:   []string{allowed},
	})
	db.SetMaxOpenConns(4)
	// the settings are global: check them on several connections of the pool
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for name, want := range map[string]string{
			"enable_external_access":       "false",
			"lock_configuration":           "true",
			"autoinstall_known_extensions": "false",
			"autoload_known_extensions":    "false",
			"parquet_metadata_cache":       "true",
			"TimeZone":                     "UTC",
			"allowed_directories":          "[" + allowed + "/, " + tmp + "/]",
		} {
			var got string
			if err := conn.QueryRowContext(ctx, "SELECT current_setting($1)::VARCHAR", name).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("connection %d: %s = %q, want %q", i, name, got, want)
			}
		}
	}

	if got := queryString(t, db, "SELECT x::VARCHAR FROM read_parquet(['"+inside+"'])"); got != "42" {
		t.Errorf("read inside = %q, want 42", got)
	}
	// a sort that spills to the temp directory still works
	if got := queryString(t, db, "SELECT count(*)::VARCHAR FROM (SELECT md5(range::VARCHAR) AS m FROM range(2000000) ORDER BY m)"); got != "2000000" {
		t.Errorf("sort = %q", got)
	}

	refused := []string{
		// a sibling that shares the directory's name as a prefix
		"SELECT * FROM read_parquet(['" + outside + "'])",
		"SELECT * FROM read_parquet(['s3://penguin-coldstore/coldstore/v1/manifest.json'])",
		"SELECT * FROM read_parquet(['http://127.0.0.1:1/x.parquet'])",
		"SELECT * FROM read_parquet(['https://example.com/x.parquet'])",
		"SELECT * FROM read_csv(['/etc/passwd'])",
		"COPY (SELECT 1) TO '" + filepath.Join(dir, "out.csv") + "'",
		"INSTALL httpfs",
		"LOAD httpfs",
		"SET enable_external_access = true",
		"SET GLOBAL enable_external_access = true",
		"SET allowed_directories = ['/']",
		"SET lock_configuration = false",
		"RESET enable_external_access",
		"ATTACH '" + filepath.Join(dir, "other.duckdb") + "' AS other",
	}
	for _, q := range refused {
		if _, err := db.Exec(q); err == nil {
			t.Errorf("%s succeeded, want it refused", q)
		}
	}
	if got := queryString(t, db, "SELECT current_setting('enable_external_access')::VARCHAR"); got != "false" {
		t.Errorf("enable_external_access = %q after the attempts, want false", got)
	}
}

func TestAllowedDirectoriesMustBeAbsolute(t *testing.T) {
	for _, cfg := range []Config{
		{AllowedDirectories: []string{"coldstore"}},
		{AllowedDirectories: []string{"/coldstore"}, TempDirectory: "tmp"},
	} {
		if db, err := Open(cfg); err == nil {
			_ = db.Close()
			t.Errorf("Open(%+v) succeeded, want error", cfg)
		}
	}
}
