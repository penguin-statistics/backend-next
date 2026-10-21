package coldstore

// Integration tests exporting from a real PostgreSQL through DuckDB's postgres extension. They are skipped unless
// PENGUIN_TEST_POSTGRES_DSN points at a disposable database and PENGUIN_TEST_DUCKDB_EXTENSION_DIR at a directory
// holding the extension (<dir>/v1.5.6/linux_amd64/postgres_scanner.duckdb_extension, as in the image), e.g.
//
//	docker run -d --rm -p 127.0.0.1:55432:5432 -e POSTGRES_PASSWORD=test postgres:14-alpine
//	PENGUIN_TEST_POSTGRES_DSN='postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable' \
//	PENGUIN_TEST_DUCKDB_EXTENSION_DIR=/opt/duckdb/extensions go test ./internal/coldstore/
//
// Every test works in a schema of its own, which it drops afterwards. The tests of ProdCheck (prodcheck_test.go) only
// need the database, so they run in CI.

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun/driver/pgdriver"
)

// testSchema is the subset of the production schema the export reads, with production's column types.
const testSchema = `
CREATE TYPE enum_drop_reports_server AS ENUM ('CN', 'US', 'JP', 'KR');
CREATE TABLE stages (
	stage_id serial PRIMARY KEY,
	ark_stage_id character varying(32) NOT NULL UNIQUE
);
CREATE TABLE drop_reports (
	report_id bigserial PRIMARY KEY,
	stage_id integer NOT NULL,
	pattern_id bigint NOT NULL,
	times integer NOT NULL,
	created_at timestamp with time zone DEFAULT now() NOT NULL,
	reliability integer NOT NULL,
	server enum_drop_reports_server NOT NULL,
	account_id bigint NOT NULL,
	source_name text,
	version text
);
CREATE INDEX drop_reports_created_at ON drop_reports (created_at);
CREATE TABLE drop_report_extras (
	report_id bigint PRIMARY KEY,
	ip cidr NOT NULL,
	metadata jsonb,
	md5 character varying(32)
);
CREATE TABLE drop_pattern_elements (
	element_id bigserial PRIMARY KEY,
	drop_pattern_id bigint NOT NULL,
	item_id integer NOT NULL,
	quantity integer NOT NULL
);
INSERT INTO stages (stage_id, ark_stage_id) VALUES (1, 'main_01-07'), (2, 'main_04-06'), (3, 'act18d0_05'), (42, 'recruit');
INSERT INTO drop_pattern_elements (drop_pattern_id, item_id, quantity)
	SELECT p, 1 + (p * 7 + q) % 50, q FROM generate_series(1, 20) p, generate_series(1, 3) q;
`

// newTestPostgres fills a fresh schema with testSchema and the test data, and returns the options to export from
// it and a connection to it.
func newTestPostgres(t *testing.T, d *testData) (Options, *sql.DB) {
	t.Helper()
	extDir := os.Getenv("PENGUIN_TEST_DUCKDB_EXTENSION_DIR")
	if extDir == "" {
		t.Skip("PENGUIN_TEST_DUCKDB_EXTENSION_DIR is not set")
	}
	dsn, db := newTestSchema(t, d)
	return Options{PostgresDSN: dsn, ExtensionDirectory: extDir, WorkDir: t.TempDir(), Threads: 2, MemoryLimit: "1GB"}, db
}

// newTestSchema fills a fresh schema with testSchema and the test data, and returns its DSN (which selects the
// schema) and a connection to it.
func newTestSchema(t *testing.T, d *testData) (string, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("PENGUIN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PENGUIN_TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("it_%d_%d", time.Now().UnixNano(), rand.Int63())

	admin := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})

	// the app's driver sends unknown DSN parameters as settings, which libpqDSN turns into options
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	dsn += sep + "search_path=" + schema
	db := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
	t.Cleanup(func() { db.Close() })
	for _, s := range append([]string{testSchema}, d.insertSQL("drop_reports", "drop_report_extras", nil)...) {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	return dsn, db
}

func TestExportFromPostgres(t *testing.T) {
	ctx := context.Background()
	d := newTestData()
	opts, pg := newTestPostgres(t, d)
	dir := t.TempDir()
	manifest := filepath.Join(dir, ManifestFile)

	for _, key := range testMonths {
		m := mustMonth(t, key)
		e, err := ExportMonth(ctx, opts, m, dir)
		if err != nil {
			t.Fatalf("export %s: %v", key, err)
		}
		checkMonthFiles(t, d, dir, e, m)
		// PostgreSQL's columns come out as the same values (and hashes) as the test data loaded into DuckDB
		if direct := writeTestMonth(t, d, t.TempDir(), m); direct.ReportsHash != e.ReportsHash || direct.ExtrasHash != e.ExtrasHash {
			t.Errorf("%s hashes from PostgreSQL %s/%s, from the test data %s/%s", key, e.ReportsHash, e.ExtrasHash, direct.ReportsHash, direct.ExtrasHash)
		}
		if err := UpdateManifest(manifest, func(mf *Manifest) error { mf.PutMonth(*e); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	dims, err := ExportDims(ctx, opts, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dims.Path != DropPatternElementsFile || dims.Rows != 60 {
		t.Errorf("dims = %+v", dims)
	}
	// a source with fewer elements than the archived file (an older restore) does not replace it
	newer := *dims
	newer.Rows++
	if _, err := ExportDims(ctx, opts, dir, &newer); err == nil || !strings.Contains(err.Error(), "fewer than the 61") {
		t.Errorf("export from an older source: %v", err)
	}
	if got, err := describeExisting(dir, DropPatternElementsFile); err != nil || got.SHA256 != dims.SHA256 {
		t.Errorf("the archived dims were replaced: %+v, %v", got, err)
	}
	if err := UpdateManifest(manifest, func(mf *Manifest) error { mf.PutDim(*dims); return nil }); err != nil {
		t.Fatal(err)
	}

	mf, err := ReadManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range testMonths {
		if err := VerifyMonth(ctx, opts, dir, mf, mustMonth(t, key), true); err != nil {
			t.Errorf("verify %s against PostgreSQL: %v", key, err)
		}
	}
	if err := VerifyDims(ctx, opts, dir, mf, true); err != nil {
		t.Errorf("verify dims: %v", err)
	}
	// new patterns after the export do not invalidate the archived elements
	if _, err := pg.ExecContext(ctx, "INSERT INTO drop_pattern_elements (drop_pattern_id, item_id, quantity) VALUES (21, 1, 1)"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDims(ctx, opts, dir, mf, true); err != nil {
		t.Errorf("verify dims after a new pattern: %v", err)
	}

	// a tampered file with a manifest that agrees with it is still caught against the source
	m := mustMonth(t, "2026-09")
	tamperFile(t, dir, KindDropReports, m.Key, "account_id = account_id + 1")
	rebuilt, err := BuildManifest(ctx, opts, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyMonth(ctx, opts, dir, rebuilt, m, false); err != nil {
		t.Errorf("verify against the rebuilt manifest only: %v", err)
	}
	problems := verificationProblems(t, VerifyMonth(ctx, opts, dir, rebuilt, m, true))
	if !strings.Contains(problems, "files vs PostgreSQL: reports_hash") || strings.Contains(problems, "rows") {
		t.Errorf("problems:\n%s", problems)
	}

	// so is a change to the source after the export, e.g. an admin marking a report unreliable
	if _, err := pg.ExecContext(ctx, `UPDATE drop_reports SET reliability = 9 WHERE report_id = (
		SELECT min(report_id) FROM drop_reports WHERE created_at >= '2026-08-01' AND created_at < '2026-09-01')`); err != nil {
		t.Fatal(err)
	}
	problems = verificationProblems(t, VerifyMonth(ctx, opts, dir, mf, mustMonth(t, "2026-08"), true))
	if !strings.Contains(problems, "files vs PostgreSQL: reports_hash") {
		t.Errorf("problems:\n%s", problems)
	}
	// re-exporting the month brings the files back in line
	e, err := ReexportMonth(ctx, opts, mustMonth(t, "2026-08"), dir, mf.Month("2026-08"))
	if err != nil {
		t.Fatal(err)
	}
	mf.PutMonth(*e)
	if err := VerifyMonth(ctx, opts, dir, mf, mustMonth(t, "2026-08"), true); err != nil {
		t.Errorf("verify after re-export: %v", err)
	}

	// once the month is gone from PostgreSQL (its partition dropped after the migration), a re-export must not
	// replace the archive, the only copy left
	if _, err := pg.ExecContext(ctx, `DELETE FROM drop_reports WHERE created_at >= '2026-08-01' AND created_at < '2026-09-01'`); err != nil {
		t.Fatal(err)
	}
	before := listFiles(t, dir)
	sums := map[string]string{}
	for _, f := range e.Files {
		got, err := describeExisting(dir, f.Path)
		if err != nil {
			t.Fatal(err)
		}
		sums[f.Path] = got.SHA256
	}
	if _, err := ReexportMonth(ctx, opts, mustMonth(t, "2026-08"), dir, e); err == nil || !strings.Contains(err.Error(), "fewer reports") {
		t.Errorf("re-export of a month gone from PostgreSQL = %v, want a refusal", err)
	}
	if after := listFiles(t, dir); strings.Join(after, " ") != strings.Join(before, " ") {
		t.Errorf("files after the refused re-export: %v, before: %v", after, before)
	}
	for _, f := range e.Files {
		if got, err := describeExisting(dir, f.Path); err != nil || got.SHA256 != sums[f.Path] {
			t.Errorf("%s changed by the refused re-export (%v)", f.Path, err)
		}
	}
}

func TestExportWithoutRecruitStage(t *testing.T) {
	d := newTestData()
	opts, pg := newTestPostgres(t, d)
	if _, err := pg.Exec("UPDATE stages SET ark_stage_id = 'not_recruit' WHERE stage_id = 42"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := ExportMonth(context.Background(), opts, mustMonth(t, "2026-09"), dir); err == nil || !strings.Contains(err.Error(), "recruit") {
		t.Errorf("export = %v, want an error about the recruit stage", err)
	}
	if files := listFiles(t, dir); len(files) != 0 {
		t.Errorf("files left: %v", files)
	}
}

// TestReconnectErrorRedactsPassword attaches PostgreSQL as a role of its own, then changes the role's password and
// ends its sessions, so that DuckDB's postgres extension has to connect again and fails: that error echoes the
// whole DSN, and must not hold any form of the password.
func TestReconnectErrorRedactsPassword(t *testing.T) {
	extDir := os.Getenv("PENGUIN_TEST_DUCKDB_EXTENSION_DIR")
	if extDir == "" {
		t.Skip("PENGUIN_TEST_DUCKDB_EXTENSION_DIR is not set")
	}
	dsn := os.Getenv("PENGUIN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PENGUIN_TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	admin := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
	t.Cleanup(func() { admin.Close() })
	adminExec := func(q string) {
		t.Helper()
		if _, err := admin.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	literal := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

	for i, p := range testPasswords {
		role := fmt.Sprintf("cs_redact_%d_%d", time.Now().UnixNano(), i)
		adminExec("CREATE ROLE " + role + " LOGIN PASSWORD " + literal(p))
		t.Cleanup(func() {
			_, _ = admin.ExecContext(context.Background(), "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = "+literal(role))
			if _, err := admin.ExecContext(context.Background(), "DROP ROLE "+role); err != nil {
				t.Error(err)
			}
		})
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword(role, p)
		opts := Options{PostgresDSN: u.String(), ExtensionDirectory: extDir, WorkDir: t.TempDir(), Threads: 1, MemoryLimit: "256MB"}
		j, err := newJob(ctx, opts, "reconnect", true)
		if err != nil {
			t.Fatalf("password %q: attach: %v", p, err)
		}
		if err := j.exec(ctx, "CREATE TABLE ok AS SELECT * FROM postgres_query('pg', $$SELECT 1 AS a$$)"); err != nil {
			j.Close()
			t.Fatalf("password %q: query before the reconnect: %v", p, err)
		}

		adminExec("ALTER ROLE " + role + " PASSWORD " + literal(p+"-changed"))
		adminExec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = " + literal(role))
		err = j.exec(ctx, "CREATE TABLE x AS SELECT * FROM postgres_query('pg', $$SELECT 1 AS a$$)")
		j.Close()
		if err == nil {
			t.Fatalf("password %q: query succeeded after the role's sessions ended with a changed password", p)
		}
		for _, f := range passwordForms(p) {
			if strings.Contains(err.Error(), f) {
				t.Errorf("password %q: %q left in %s", p, f, err)
			}
		}
	}
}
