package coldstore

// Integration tests of the monthly archiving on the schema of the new database (scripts/newdb): drop_reports (the
// recruitment reports included) and drop_report_extras partitioned by month of created_at. Like export_integration_test.go, they are skipped unless PENGUIN_TEST_POSTGRES_DSN and
// PENGUIN_TEST_DUCKDB_EXTENSION_DIR are set; the bucket is an in-memory fake that checks every upload's SHA-256 as S3
// does (s3_integration_test.go runs the S3 client against an S3-compatible server).

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/uptrace/bun/driver/pgdriver"
)

// newdbTestSchema is the part of scripts/newdb/schema.sql (the schema of the new database) that the archiving reads
// and changes, with the same tables, keys and create_report_partitions.
const newdbTestSchema = `
CREATE TYPE enum_drop_reports_server AS ENUM ('CN', 'US', 'JP', 'KR');
CREATE SEQUENCE drop_reports_report_id_seq;
CREATE TABLE drop_reports (
	report_id bigint DEFAULT nextval('drop_reports_report_id_seq') NOT NULL,
	stage_id integer NOT NULL,
	pattern_id bigint NOT NULL,
	times integer NOT NULL,
	created_at timestamp with time zone DEFAULT now() NOT NULL,
	reliability integer NOT NULL,
	server enum_drop_reports_server NOT NULL,
	account_id bigint NOT NULL,
	source_name text,
	version text,
	CONSTRAINT drop_reports_pkey PRIMARY KEY (report_id, created_at)
) PARTITION BY RANGE (created_at);
CREATE INDEX drop_reports_created_at ON drop_reports (created_at);
CREATE TABLE drop_report_extras (
	report_id bigint NOT NULL,
	ip cidr NOT NULL,
	metadata jsonb,
	md5 character varying(32),
	created_at timestamp with time zone NOT NULL,
	CONSTRAINT drop_report_extras_pkey PRIMARY KEY (report_id, created_at)
) PARTITION BY RANGE (created_at);
CREATE FUNCTION create_report_partitions(p_month date) RETURNS void
	LANGUAGE plpgsql
	AS $$
DECLARE
	month_start timestamp := date_trunc('month', p_month::timestamp);
	lo text := to_char(month_start, 'YYYY-MM-DD HH24:MI:SS') || '+00';
	hi text := to_char(month_start + interval '1 month', 'YYYY-MM-DD HH24:MI:SS') || '+00';
	t text;
BEGIN
	FOREACH t IN ARRAY ARRAY['drop_reports', 'drop_report_extras'] LOOP
		EXECUTE format('CREATE TABLE IF NOT EXISTS %I PARTITION OF %I FOR VALUES FROM (%L) TO (%L)',
			t || '_' || to_char(month_start, 'YYYY_MM'), t, lo, hi);
	END LOOP;
END;
$$;
CREATE TABLE stages (
	stage_id serial PRIMARY KEY,
	ark_stage_id character varying(32) NOT NULL UNIQUE
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

// newTestNewDB creates a schema with newdbTestSchema, the partitions of the given months, and the test data's
// reports of those months in drop_reports (the recruit ones included), with their extras in drop_report_extras
// under the created_at of their report.
func newTestNewDB(t *testing.T, d *testData, months ...string) (Options, *sql.DB) {
	t.Helper()
	extDir := os.Getenv("PENGUIN_TEST_DUCKDB_EXTENSION_DIR")
	if extDir == "" {
		t.Skip("PENGUIN_TEST_DUCKDB_EXTENSION_DIR is not set")
	}
	dsn, db := newEmptyTestSchema(t)
	loadNewDB(t, db, d, months...)
	return Options{PostgresDSN: dsn, ExtensionDirectory: extDir, WorkDir: t.TempDir(), Threads: 2, MemoryLimit: "1GB"}, db
}

// loadNewDB creates newdbTestSchema in db's search_path, with the partitions of the given months and the test data's
// reports of those months (see newTestNewDB).
func loadNewDB(t *testing.T, db *sql.DB, d *testData, months ...string) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{newdbTestSchema}
	keep := map[string]bool{}
	for _, m := range months {
		keep[m] = true
		stmts = append(stmts, "SELECT create_report_partitions(date '"+m+"-01')")
	}
	extras := map[int64]testExtra{}
	for _, e := range d.extras {
		extras[e.id] = e
	}
	var reports, extraRows []string
	for _, r := range d.reports {
		if !keep[MonthOf(r.createdAt).Key] {
			continue
		}
		reports = append(reports, fmt.Sprintf("(%d, %d, %d, %d, %s, %d, %s, %d, %s, %s)", r.id, r.stage, r.pattern, r.times,
			sqlTime(r.createdAt), r.reliability, quote(r.server), r.account, sqlNullable(r.source), sqlNullable(r.version)))
		if e, ok := extras[r.id]; ok {
			extraRows = append(extraRows, fmt.Sprintf("(%d, %s, %s, %s, %s)", e.id, quote(e.ip), sqlNullable(e.metadata),
				sqlNullable(e.md5), sqlTime(r.createdAt)))
		}
	}
	stmts = append(stmts,
		"INSERT INTO drop_reports ("+reportColumns+") VALUES "+strings.Join(reports, ", "),
		"INSERT INTO drop_report_extras ("+extrasColumns+", created_at) VALUES "+strings.Join(extraRows, ", "))
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", firstLine(s), err)
		}
	}
}

// fakeBucket is an in-memory Bucket that, like S3, rejects a body whose SHA-256 is not the one sent with it.
type fakeBucket struct {
	mu      sync.Mutex
	objects map[string]fakeObject
	puts    []string
	// failPut, if set, fails the PUTs of the keys it returns an error for
	failPut func(key string) error
	// corrupt, if set, makes the bucket store other bytes than it was sent for the keys it returns true for, as
	// if the upload were corrupted after S3's check (HEAD then reports another SHA-256)
	corrupt func(key string) bool
}

type fakeObject struct {
	data         []byte
	storageClass string
	modified     time.Time
}

func newFakeBucket() *fakeBucket {
	return &fakeBucket{objects: map[string]fakeObject{}}
}

func (b *fakeBucket) info(o fakeObject) *ObjectInfo {
	sha := sha256.Sum256(o.data)
	sum := md5.Sum(o.data)
	return &ObjectInfo{Size: int64(len(o.data)), SHA256: hex.EncodeToString(sha[:]), ETag: `"` + hex.EncodeToString(sum[:]) + `"`,
		LastModified: o.modified, StorageClass: o.storageClass}
}

func (b *fakeBucket) Head(_ context.Context, key string) (*ObjectInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	o, ok := b.objects[key]
	if !ok {
		return nil, nil
	}
	return b.info(o), nil
}

func (b *fakeBucket) Put(_ context.Context, key string, body io.ReadSeeker, size int64, sha256Hex, storageClass string) error {
	if b.failPut != nil {
		if err := b.failPut(key); err != nil {
			return err
		}
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != size || hex.EncodeToString(sum[:]) != sha256Hex {
		return errors.New("BadDigest")
	}
	if b.corrupt != nil && b.corrupt(key) {
		data = append(data[:len(data):len(data)], 0)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objects[key] = fakeObject{data: data, storageClass: storageClass, modified: time.Now()}
	b.puts = append(b.puts, key)
	return nil
}

func (b *fakeBucket) Copy(_ context.Context, src, dst, etag string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	o, ok := b.objects[src]
	if !ok || b.info(o).ETag != etag {
		return errors.New("PreconditionFailed")
	}
	b.objects[dst] = fakeObject{data: o.data, storageClass: manifestStorageClass, modified: time.Now()}
	return nil
}

func (b *fakeBucket) keys(prefix string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for k := range b.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// partitionRows returns the partitions of the report tables with their row counts, as "name=rows".
func partitionRows(t *testing.T, db *sql.DB) []string {
	t.Helper()
	ctx := context.Background()
	months, parts, _, err := MonthPartitions(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, key := range months {
		for _, p := range parts[key] {
			var n int64
			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+p.QualifiedName()).Scan(&n); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprintf("%s=%d", p.Name, n))
		}
	}
	return out
}

// seedColdstore fills dir and the bucket b as the backfill leaves them: every month from pre to through, with
// small stand-in files (the archiving reads only the files of the months it archives), and the manifest.
func seedColdstore(t *testing.T, dir string, b *fakeBucket, through string) {
	t.Helper()
	m := NewManifest()
	put := func(p string, data []byte) FileEntry {
		name := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, data, 0o644); err != nil {
			t.Fatal(err)
		}
		b.objects[path.Join(Prefix, p)] = fakeObject{data: data, storageClass: "STANDARD_IA", modified: time.Now()}
		sum := sha256.Sum256(data)
		return FileEntry{Path: p, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data))}
	}
	keys := []string{PreMonth}
	for start := Epoch; start.Format("2006-01") <= through; start = start.AddDate(0, 1, 0) {
		keys = append(keys, start.Format("2006-01"))
	}
	for _, key := range keys {
		e := MonthEntry{Month: key, GeneratedAt: time.Now().UTC(), Generator: "seed"}
		for _, kind := range Kinds {
			e.Files = append(e.Files, put(FilePath(kind, key), []byte("seed "+FilePath(kind, key))))
		}
		m.PutMonth(e)
	}
	m.PutDim(put(DropPatternElementsFile, []byte("seed dims")))
	content, err := MarshalManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeManifestFile(filepath.Join(dir, ManifestFile), content); err != nil {
		t.Fatal(err)
	}
	b.objects[path.Join(Prefix, ManifestFile)] = fakeObject{data: content, storageClass: manifestStorageClass, modified: time.Now()}
}

// bucketManifest returns the manifest.json of the bucket.
func bucketManifest(t *testing.T, b *fakeBucket) *Manifest {
	t.Helper()
	b.mu.Lock()
	o, ok := b.objects[path.Join(Prefix, ManifestFile)]
	b.mu.Unlock()
	if !ok {
		t.Fatal("no manifest in the bucket")
	}
	var m Manifest
	if err := json.Unmarshal(o.data, &m); err != nil {
		t.Fatal(err)
	}
	return &m
}

// newTestArchiver returns an archiver of a new cold storage directory that holds every month up to through (see
// seedColdstore), at the time *now.
func newTestArchiver(t *testing.T, opts Options, db *sql.DB, b *fakeBucket, now *time.Time, through string) *Archiver {
	t.Helper()
	dir := t.TempDir()
	seedColdstore(t, dir, b, through)
	a, err := NewArchiver(ArchiveOptions{
		Options:     opts,
		Dir:         dir,
		After:       60 * 24 * time.Hour,
		Backup:      BackupOptions{Prefix: Prefix, StorageClass: "STANDARD_IA"},
		LockTimeout: 500 * time.Millisecond,
		LockTries:   2,
		Now:         func() time.Time { return *now },
	}, db, b)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func setArchiveHook(t *testing.T, hook func(month, step string) error) {
	t.Helper()
	testHookArchiveStep = hook
	t.Cleanup(func() { testHookArchiveStep = nil })
}

func TestArchiveMonthly(t *testing.T) {
	ctx := context.Background()
	d := newTestData()
	opts, db := newTestNewDB(t, d, "2026-07", "2026-08", "2026-09")
	b := newFakeBucket()
	// 2026-07 (empty) and 2026-08 ended more than 60 days ago, 2026-09 did not
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	recordedAt := now
	a := newTestArchiver(t, opts, db, b, &now, "2026-06")
	dir := a.opts.Dir

	// a month patched locally since its upload: the bucket gets its new file before a manifest that lists it
	patched := FilePath(KindRollup, "2020-01")
	if err := os.WriteFile(filepath.Join(dir, patched), []byte("patched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := updateManifest(filepath.Join(dir, ManifestFile), func(m *Manifest) error {
		fe := m.Month("2020-01").File(KindRollup)
		sum := sha256.Sum256([]byte("patched"))
		fe.SHA256, fe.Bytes = hex.EncodeToString(sum[:]), 7
		return nil
	}, nil); err != nil {
		t.Fatal(err)
	}

	// the first run records 2026-07 and keeps its partitions until the readers can have loaded the manifest, then
	// stops (as if killed) once 2026-08 is in both manifests
	setArchiveHook(t, func(month, step string) error {
		if step == "recorded" && month == "2026-08" {
			return errors.New("crash")
		}
		return nil
	})
	res, err := a.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "crash") {
		t.Fatalf("first run = %+v, %v", res, err)
	}
	if !res.PartitionsCreated || len(res.Archived) != 0 || strings.Join(res.Pending, " ") != "2026-07" {
		t.Errorf("first run = %+v", res)
	}
	parts := strings.Join(partitionRows(t, db), " ")
	for _, kept := range []string{"drop_reports_2026_07=0", "drop_report_extras_2026_07=0", "drop_reports_2026_08=",
		"drop_report_extras_2026_08=", "drop_reports_2026_09=", "drop_reports_2026_11=0", "drop_reports_2026_12=0",
		"drop_report_extras_2027_01=0"} {
		if !strings.Contains(parts, kept) {
			t.Errorf("%s missing: %s", kept, parts)
		}
	}
	if strings.Contains(parts, "recruit") || len(partitionRows(t, db)) != 2*6 {
		t.Errorf("partitions: %s", parts)
	}
	mf, err := ReadManifest(filepath.Join(dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	first := mf.Month("2026-08")
	if first == nil || mf.Month("2026-07") == nil {
		t.Fatal("2026-07 or 2026-08 is not in the local manifest")
	}
	for _, key := range []string{"2026-07", "2026-08"} {
		if e := mf.Month(key); !e.RecordedAt.Equal(recordedAt) {
			t.Errorf("%s recorded at %v, want %v", key, e.RecordedAt, recordedAt)
		}
	}
	if bm := bucketManifest(t, b); bm.Month("2026-08") == nil {
		t.Error("2026-08 is not in the bucket's manifest")
	}
	if o := b.objects[path.Join(Prefix, patched)]; string(o.data) != "patched" {
		t.Errorf("the patched file was not uploaded: %q", o.data)
	}

	// before DropDelay has passed, a run does nothing with the recorded months
	testHookArchiveStep = nil
	putsBefore := len(b.puts)
	res, err = a.Run(ctx)
	if err != nil || len(res.Archived) != 0 || strings.Join(res.Pending, " ") != "2026-07 2026-08" {
		t.Fatalf("second run = %+v, %v", res, err)
	}
	if after := strings.Join(partitionRows(t, db), " "); after != parts || len(b.puts) != putsBefore {
		t.Errorf("second run changed something: partitions %s, uploads %v", after, b.puts[putsBefore:])
	}

	// then the next run checks the months again and drops their partitions
	now = now.Add(DefaultDropDelay)
	res, err = a.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Archived, " ") != "2026-07 2026-08" || len(res.Pending) != 0 ||
		strings.Join(res.Hot, " ") != "2026-09 2026-11 2026-12 2027-01" {
		t.Errorf("third run = %+v", res)
	}
	parts = strings.Join(partitionRows(t, db), " ")
	if strings.Contains(parts, "2026_07") || strings.Contains(parts, "2026_08") || !strings.Contains(parts, "drop_reports_2026_09=") {
		t.Errorf("partitions after the third run: %s", parts)
	}
	// nothing was uploaded again, not even the manifest, which did not change
	if len(b.puts) != putsBefore {
		t.Errorf("uploaded again: %v", b.puts[putsBefore:])
	}

	mf, err = ReadManifest(filepath.Join(dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if last := mf.Months[len(mf.Months)-1].Month; last != "2026-08" || len(mf.Dims) != 1 || mf.Month("2019-05") == nil {
		t.Fatalf("manifest: last month %s, %d dims", last, len(mf.Dims))
	}
	if e := mf.Month("2026-08"); e == nil || !sameFiles(e, first) {
		t.Errorf("2026-08 changed: %+v", e)
	}
	// the bucket has every file of the manifest, and the manifest itself, byte for byte
	local, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := b.objects[path.Join(Prefix, ManifestFile)]; !bytes.Equal(got.data, local) || got.storageClass != "STANDARD" {
		t.Errorf("bucket manifest differs from the local one (class %q)", got.storageClass)
	}
	for _, fe := range manifestFiles(mf) {
		o, ok := b.objects[path.Join(Prefix, fe.Path)]
		if !ok || b.info(o).SHA256 != fe.SHA256 || o.storageClass != "STANDARD_IA" {
			t.Errorf("%s in the bucket: %v %+v", fe.Path, ok, b.info(o))
		}
	}
	// the replaced manifests are kept
	if versions := b.keys(path.Join(Prefix, ManifestVersionsDir) + "/"); len(versions) < 2 {
		t.Errorf("kept manifests: %v", versions)
	}

	for _, key := range []string{"2026-07", "2026-08"} {
		if err := VerifyMonth(ctx, opts, dir, mf, mustMonth(t, key), false); err != nil {
			t.Errorf("verify %s: %v", key, err)
		}
	}
	// the month has the files and stats of the same month exported from today's database by the backfill (A2's
	// ExportMonth): the recruit reports in the recruit file with their pattern_id, byte for byte
	m := mustMonth(t, "2026-08")
	e := mf.Month("2026-08")
	checkMonthFiles(t, d, dir, e, m)
	oldOpts, _ := newTestPostgres(t, d)
	backfilled, err := ExportMonth(ctx, oldOpts, m, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if p := backfilled.MonthStats.diff(e.MonthStats); !sameFiles(e, backfilled) || len(p) > 0 || e.RecruitStageID != backfilled.RecruitStageID {
		t.Errorf("2026-08 archived from the new database differs from its backfill export (%v):\n%+v\n%+v", p, e.Files, backfilled.Files)
	}
	r := reader(t)
	if got := queryStrings(t, r, `SELECT count(*) FILTER (WHERE pattern_id IS NOT NULL)::VARCHAR FROM read_parquet([`+
		quote(filepath.Join(dir, FilePath(KindRecruit, "2026-08")))+`])`); got[0] != fmt.Sprint(d.expected(m).RecruitRows) {
		t.Errorf("recruit rows with a pattern_id: %s, want %d", got[0], d.expected(m).RecruitRows)
	}
	for _, kind := range []Kind{KindDropReports, KindRecruit} {
		if n := queryStrings(t, r, `SELECT count(*)::VARCHAR FROM parquet_schema([`+quote(filepath.Join(dir, FilePath(kind, "2026-08")))+`])
			WHERE name = 'item_ids'`); n[0] != "0" {
			t.Errorf("%s file has item_ids", kind)
		}
	}

	// nothing left to archive: a run only makes sure of the partitions ahead
	res, err = a.Run(ctx)
	if err != nil || len(res.Archived) != 0 || len(res.Pending) != 0 {
		t.Errorf("third run = %+v, %v", res, err)
	}
}

// Every failure before the drop leaves the month's partitions as they were, and the month out of the local
// manifest unless it got there.
func TestArchiveFailuresDropNothing(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC) // 2026-08 is due, 2026-09 is not
	cases := []struct {
		name    string
		setup   func(t *testing.T, a *Archiver, b *fakeBucket, db *sql.DB)
		outcome string
		errText string
		// recorded: the failure comes after the month was recorded in both manifests (by an earlier run, DropDelay
		// before this one)
		recorded bool
		// final: the cause cannot go away by itself (the data or the directory changed), the month is not archived
		// again
		final bool
	}{
		{
			name: "upload fails",
			setup: func(t *testing.T, a *Archiver, b *fakeBucket, db *sql.DB) {
				b.failPut = func(key string) error {
					if strings.Contains(key, "recruit_") {
						return errors.New("connection reset")
					}
					return nil
				}
			},
			outcome: "error",
			errText: "connection reset",
		},
		{
			name: "upload corrupted",
			setup: func(t *testing.T, a *Archiver, b *fakeBucket, db *sql.DB) {
				b.corrupt = func(key string) bool { return strings.Contains(key, "rollup_") }
			},
			outcome: "verification_failed",
			errText: "rollup_2026-08.parquet",
		},
		{
			name: "manifest upload fails",
			setup: func(t *testing.T, a *Archiver, b *fakeBucket, db *sql.DB) {
				b.failPut = func(key string) error {
					if strings.HasSuffix(key, ManifestFile) {
						return errors.New("access denied")
					}
					return nil
				}
			},
			outcome: "error",
			errText: "access denied",
		},
		{
			name: "file corrupted after its export",
			setup: func(t *testing.T, a *Archiver, b *fakeBucket, db *sql.DB) {
				setArchiveHook(t, func(_, step string) error {
					if step == "exported" {
						f, err := os.OpenFile(filepath.Join(a.opts.Dir, FilePath(KindExtras, "2026-08")), os.O_WRONLY|os.O_APPEND, 0)
						if err != nil {
							return err
						}
						defer f.Close()
						_, err = f.Write([]byte{0})
						return err
					}
					return nil
				})
			},
			outcome: "verification_failed",
			errText: "extras_2026-08.parquet",
		},
		{
			name: "PostgreSQL changed after the export",
			setup: func(t *testing.T, a *Archiver, b *fakeBucket, db *sql.DB) {
				setArchiveHook(t, func(_, step string) error {
					if step == "uploaded" {
						_, err := db.Exec(`UPDATE drop_reports SET reliability = 9 WHERE report_id = (
							SELECT min(report_id) FROM drop_reports WHERE stage_id = 42 AND created_at >= '2026-08-01' AND created_at < '2026-09-01')`)
						return err
					}
					return nil
				})
			},
			outcome: "verification_failed",
			errText: "files vs PostgreSQL: reports_hash",
			final:   true,
		},
		{
			name: "extras without a report",
			setup: func(t *testing.T, a *Archiver, b *fakeBucket, db *sql.DB) {
				if _, err := db.Exec(`INSERT INTO drop_report_extras (report_id, ip, created_at) VALUES (999999, '192.0.2.0/24', '2026-08-10')`); err != nil {
					t.Fatal(err)
				}
			},
			outcome: "verification_failed",
			errText: "1 drop_report_extras rows of the month belong to no report",
			final:   true,
		},
		{
			name: "month before it not archived",
			setup: func(t *testing.T, a *Archiver, b *fakeBucket, db *sql.DB) {
				if err := updateManifest(filepath.Join(a.opts.Dir, ManifestFile), func(m *Manifest) error {
					m.Months = m.Months[:len(m.Months)-1] // 2026-07
					return nil
				}, nil); err != nil {
					t.Fatal(err)
				}
			},
			outcome: "error",
			errText: "the last month of the local manifest is 2026-06, not 2026-07",
			final:   true,
		},
		{
			name: "directory without its manifest",
			setup: func(t *testing.T, a *Archiver, b *fakeBucket, db *sql.DB) {
				// e.g. a mount point whose disk is gone: the archiving would replace the bucket's manifest with one
				// that has a single month
				if err := os.Remove(filepath.Join(a.opts.Dir, ManifestFile)); err != nil {
					t.Fatal(err)
				}
			},
			outcome: "error",
			errText: "has no manifest.json",
			final:   true,
		},
		{
			name: "file of an earlier month missing",
			setup: func(t *testing.T, a *Archiver, b *fakeBucket, db *sql.DB) {
				// the bucket still has it, but the readers would refuse a manifest that lists it: the month is not
				// published
				if err := os.Remove(filepath.Join(a.opts.Dir, FilePath(KindRollup, "2020-01"))); err != nil {
					t.Fatal(err)
				}
			},
			outcome: "error",
			errText: FilePath(KindRollup, "2020-01") + " is missing",
			final:   true,
		},
		{
			name: "file of an earlier month missing before the drop",
			setup: func(t *testing.T, a *Archiver, b *fakeBucket, db *sql.DB) {
				// the readers cannot load the manifest with the month: they keep reading it from PostgreSQL
				if err := os.Remove(filepath.Join(a.opts.Dir, FilePath(KindRollup, "2020-01"))); err != nil {
					t.Fatal(err)
				}
			},
			outcome:  "error",
			errText:  "the readers cannot serve the local manifest, keeping the partitions",
			recorded: true,
			final:    true,
		},
		{
			name: "not enough free space",
			setup: func(t *testing.T, a *Archiver, b *fakeBucket, db *sql.DB) {
				a.opts.MinFreeBytes = 1 << 60
			},
			outcome: "insufficient_space",
			errText: "not archiving 2026-08",
		},
		{
			name: "partition locked",
			setup: func(t *testing.T, a *Archiver, b *fakeBucket, db *sql.DB) {
				// a writer of the month that does not finish
				tx, err := db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = tx.Rollback() })
				if _, err := tx.Exec(`UPDATE drop_reports_2026_08 SET reliability = reliability WHERE report_id = (SELECT min(report_id) FROM drop_reports_2026_08)`); err != nil {
					t.Fatal(err)
				}
			},
			outcome: "error",
			errText: "lock timeout",
			final:   true,
		},
		{
			name: "drop waits for a query",
			setup: func(t *testing.T, a *Archiver, b *fakeBucket, db *sql.DB) {
				// a long query of drop_reports, which DETACH has to wait for
				tx, err := db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = tx.Rollback() })
				var n int64
				if err := tx.QueryRow(`SELECT count(*) FROM drop_reports`).Scan(&n); err != nil {
					t.Fatal(err)
				}
			},
			outcome:  "error",
			errText:  "lock timeout",
			recorded: true,
			final:    true,
		},
	}
	d := newTestData()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts, db := newTestNewDB(t, d, "2026-08", "2026-09")
			b := newFakeBucket()
			now := now
			a := newTestArchiver(t, opts, db, b, &now, "2026-07")
			// the partitions ahead, so that a run has none to create
			if _, err := CreatePartitions(ctx, db, now, time.Second); err != nil {
				t.Fatal(err)
			}
			if c.recorded {
				if res, err := a.Run(ctx); err != nil || strings.Join(res.Pending, " ") != "2026-08" {
					t.Fatalf("run that records the month = %+v, %v", res, err)
				}
				now = now.Add(DefaultDropDelay)
			}
			c.setup(t, a, b, db)
			before := partitionRows(t, db)

			_, err := a.Run(ctx)
			if err == nil || !strings.Contains(err.Error(), c.errText) {
				t.Fatalf("run = %v, want an error with %q", err, c.errText)
			}
			if got := ArchiveOutcome(err); got != c.outcome {
				t.Errorf("outcome = %s, want %s (%v)", got, c.outcome, err)
			}
			if after := partitionRows(t, db); strings.Join(after, " ") != strings.Join(before, " ") {
				t.Errorf("partitions after: %v, before: %v", after, before)
			}
			mf, err := ReadManifest(filepath.Join(a.opts.Dir, ManifestFile))
			if inLocal := err == nil && mf.Month("2026-08") != nil; inLocal != c.recorded {
				t.Errorf("2026-08 in the local manifest: %v", inLocal)
			}
			if bm := bucketManifest(t, b); (bm.Month("2026-08") != nil) != c.recorded || bm.Month(PreMonth) == nil || bm.Month("2026-06") == nil {
				t.Errorf("bucket manifest: 2026-08 %v, pre %v, 2026-06 %v", bm.Month("2026-08") != nil, bm.Month(PreMonth) != nil,
					bm.Month("2026-06") != nil)
			}
			if c.final {
				return
			}

			// the month is archived once the cause is gone: recorded by a run, dropped by a run DropDelay later
			testHookArchiveStep = nil
			b.failPut, b.corrupt = nil, nil
			a.opts.MinFreeBytes = 0
			res, err := a.Run(ctx)
			if err != nil || strings.Join(res.Pending, " ") != "2026-08" {
				t.Fatalf("run after the failure = %+v, %v", res, err)
			}
			now = now.Add(DefaultDropDelay)
			res, err = a.Run(ctx)
			if err != nil || strings.Join(res.Archived, " ") != "2026-08" {
				t.Fatalf("run DropDelay later = %+v, %v", res, err)
			}
		})
	}
}

// The DropDelay of a month starts once it is in the local manifest, however long the uploads before took, and the
// run that publishes the month keeps its partitions, also when it stops before recording the time or when the upload
// of the recorded time takes longer than DropDelay.
func TestArchiveDropDelayStartsAtPublication(t *testing.T) {
	ctx := context.Background()
	d := newTestData()
	cases := []struct {
		name string
		// setup is called with the archiver and its clock before the first run, which fails with crash if crash is
		// set. It sets *at to the time the month must be recorded at, if not the time after that run.
		setup func(t *testing.T, a *Archiver, b *fakeBucket, now, at *time.Time)
		crash string
	}{
		{
			name: "slow manifest upload",
			setup: func(t *testing.T, a *Archiver, b *fakeBucket, now, at *time.Time) {
				// the first upload of manifest.json takes longer than DropDelay
				slow := true
				b.failPut = func(key string) error {
					if slow && key == path.Join(Prefix, ManifestFile) {
						slow = false
						*now = now.Add(DefaultDropDelay + time.Second)
					}
					return nil
				}
			},
		},
		{
			name: "slow upload of the recorded time",
			setup: func(t *testing.T, a *Archiver, b *fakeBucket, now, at *time.Time) {
				// the second upload of manifest.json, which holds the recorded time, takes longer than DropDelay:
				// the run that records the time keeps the partitions all the same
				puts := 0
				b.failPut = func(key string) error {
					if key == path.Join(Prefix, ManifestFile) {
						if puts++; puts == 2 {
							*at = *now
							*now = now.Add(DefaultDropDelay + time.Second)
						}
					}
					return nil
				}
			},
		},
		{
			name: "stopped before recording the time",
			setup: func(t *testing.T, a *Archiver, b *fakeBucket, now, at *time.Time) {
				setArchiveHook(t, func(month, step string) error {
					if step == "published" {
						return errors.New("crash")
					}
					return nil
				})
			},
			crash: "crash",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts, db := newTestNewDB(t, d, "2026-08", "2026-09")
			b := newFakeBucket()
			now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC) // 2026-08 is due, 2026-09 is not
			a := newTestArchiver(t, opts, db, b, &now, "2026-07")
			if _, err := CreatePartitions(ctx, db, now, time.Second); err != nil {
				t.Fatal(err)
			}
			before := strings.Join(partitionRows(t, db), " ")
			kept := func(run string) {
				t.Helper()
				if after := strings.Join(partitionRows(t, db), " "); after != before {
					t.Fatalf("%s: partitions %s, before %s", run, after, before)
				}
			}
			recordedAt := func() time.Time {
				t.Helper()
				mf, err := ReadManifest(filepath.Join(a.opts.Dir, ManifestFile))
				if err != nil {
					t.Fatal(err)
				}
				if mf.Month("2026-08") == nil {
					t.Fatal("2026-08 is not in the local manifest")
				}
				return mf.Month("2026-08").RecordedAt
			}

			var at time.Time
			c.setup(t, a, b, &now, &at)
			res, err := a.Run(ctx)
			if c.crash != "" {
				if err == nil || !strings.Contains(err.Error(), c.crash) {
					t.Fatalf("first run = %+v, %v", res, err)
				}
				kept("first run")
				if at := recordedAt(); !at.IsZero() {
					t.Fatalf("2026-08 recorded at %v by the run that stopped", at)
				}
				if bucketManifest(t, b).Month("2026-08") == nil {
					t.Fatal("2026-08 is not in the bucket's manifest")
				}
				// long after the publication, the next run cannot know when it was: it records the time then
				testHookArchiveStep = nil
				now = now.Add(2 * DefaultDropDelay)
				res, err = a.Run(ctx)
			}
			if err != nil || len(res.Archived) != 0 || strings.Join(res.Pending, " ") != "2026-08" {
				t.Fatalf("run that records the time = %+v, %v", res, err)
			}
			kept("run that records the time")
			if at.IsZero() {
				at = now
			}
			if got := recordedAt(); !got.Equal(at) {
				t.Errorf("2026-08 recorded at %v, want %v (after its publication)", got, at)
			}
			local, err := os.ReadFile(filepath.Join(a.opts.Dir, ManifestFile))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(b.objects[path.Join(Prefix, ManifestFile)].data, local) {
				t.Error("bucket manifest differs from the local one")
			}

			// a run right after keeps the partitions (unless DropDelay has passed since the recorded time), a run
			// DropDelay after the recorded time drops them
			if now.Before(at.Add(DefaultDropDelay)) {
				if res, err = a.Run(ctx); err != nil || len(res.Archived) != 0 || strings.Join(res.Pending, " ") != "2026-08" {
					t.Fatalf("run right after = %+v, %v", res, err)
				}
				kept("run right after")
				now = at.Add(DefaultDropDelay)
			}
			if res, err = a.Run(ctx); err != nil || strings.Join(res.Archived, " ") != "2026-08" {
				t.Fatalf("run DropDelay later = %+v, %v", res, err)
			}
		})
	}
}

// A writer that updates drop_reports by report_id alone locks the partitioned table, then waits for the SHARE locks
// on the month's partitions, which DETACH would wait for in turn: the archiving releases its locks before such a
// DETACH, so that the writer finishes, then locks and verifies the month again and drops it. The writer comes either
// during the verification, or while the first DETACH (drop_report_extras') waits for a reader of the partition.
func TestArchiveDropLetsParentWriterFinish(t *testing.T) {
	ctx := context.Background()
	d := newTestData()
	for _, during := range []string{"verification", "first detach"} {
		t.Run(during, func(t *testing.T) {
			opts, db := newTestNewDB(t, d, "2026-08", "2026-09")
			b := newFakeBucket()
			now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
			a := newTestArchiver(t, opts, db, b, &now, "2026-07")
			a.opts.LockTimeout, a.opts.LockTries = 3*time.Second, 3
			// neither session is ever the victim of a deadlock check: only the archiving releasing its locks before
			// the DETACH lets the writer finish before a lock_timeout
			a.db = sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(opts.PostgresDSN + "&deadlock_timeout=60s")))
			t.Cleanup(func() { a.db.Close() })
			if _, err := CreatePartitions(ctx, db, now, time.Second); err != nil {
				t.Fatal(err)
			}
			if res, err := a.Run(ctx); err != nil || strings.Join(res.Pending, " ") != "2026-08" {
				t.Fatalf("run that records the month = %+v, %v", res, err)
			}
			now = now.Add(DefaultDropDelay)

			var id int64
			if err := db.QueryRowContext(ctx, `SELECT min(report_id) FROM drop_reports_2026_08`).Scan(&id); err != nil {
				t.Fatal(err)
			}
			writer, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			if _, err := writer.ExecContext(ctx, `SET deadlock_timeout = '60s'`); err != nil {
				t.Fatal(err)
			}
			// waitFor waits until a query waits for a lock (of mode, if not empty) on rel
			waitFor := func(rel, mode string) error {
				for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
					var waiting bool
					if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM pg_locks WHERE NOT granted
						AND relation = $1::regclass AND ($2 = '' OR mode = $2))`, rel, mode).Scan(&waiting); err != nil {
						return err
					}
					if waiting {
						return nil
					}
					if time.Now().After(deadline) {
						return fmt.Errorf("nothing waits for %s %s", rel, mode)
					}
				}
			}
			type result struct {
				err error
				at  time.Time
			}
			done := make(chan result, 1)
			// blocked receives when the writer started waiting for the partition, or why it did not
			blocked := make(chan result, 1)
			startWriter := func() {
				go func() {
					_, err := writer.ExecContext(ctx, `UPDATE drop_reports SET reliability = reliability WHERE report_id = $1`, id)
					done <- result{err, time.Now()}
				}()
				err := waitFor("drop_reports_2026_08", "")
				blocked <- result{err, time.Now()}
			}
			var once sync.Once
			switch during {
			case "verification":
				setArchiveHook(t, func(month, step string) error {
					if step == "verified" {
						once.Do(startWriter)
					}
					return nil
				})
			case "first detach":
				// the reader holds up the DETACH of drop_report_extras_2026_08 (the first one), which holds the lock
				// of drop_report_extras, not yet the one of drop_reports: the writer comes, then the reader ends
				reader, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Rollback() //nolint:errcheck
				if _, err := reader.ExecContext(ctx, `SELECT FROM drop_report_extras_2026_08 LIMIT 1`); err != nil {
					t.Fatal(err)
				}
				setArchiveHook(t, func(month, step string) error {
					if step == "verified" {
						once.Do(func() {
							go func() {
								if err := waitFor("drop_report_extras_2026_08", "AccessExclusiveLock"); err != nil {
									blocked <- result{err: err}
								} else {
									startWriter()
								}
								reader.Rollback() //nolint:errcheck
							}()
						})
					}
					return nil
				})
			}

			res, err := a.Run(ctx)
			if err != nil || strings.Join(res.Archived, " ") != "2026-08" {
				t.Fatalf("run = %+v, %v", res, err)
			}
			var blockedAt time.Time
			select {
			case r := <-blocked:
				if r.err != nil {
					t.Fatal(r.err)
				}
				blockedAt = r.at
			default:
				t.Fatal("the writer did not start")
			}
			select {
			case r := <-done:
				if r.err != nil {
					t.Fatalf("writer: %v", r.err)
				}
				// released right away, not after the DETACH waited for its lock_timeout
				if waited := r.at.Sub(blockedAt); waited >= a.opts.LockTimeout {
					t.Errorf("the writer waited %v", waited)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the writer did not finish")
			}
			if parts := strings.Join(partitionRows(t, db), " "); strings.Contains(parts, "2026_08") {
				t.Errorf("partitions: %s", parts)
			}
		})
	}
}

// waitForLockWait waits until a query of db's database waits for a lock of mode (any, if empty) on rel.
func waitForLockWait(ctx context.Context, db *sql.DB, rel, mode string) error {
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var waiting bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM pg_locks WHERE NOT granted
			AND relation = $1::regclass AND ($2 = '' OR mode = $2))`, rel, mode).Scan(&waiting); err != nil {
			return err
		}
		if waiting {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("nothing waits for %s %s", rel, mode)
		}
	}
}

// A late report of the month with a new pattern can come in after the drop pattern elements were exported and before
// the month is: the month is tried again, and published only with drop pattern elements that hold the new pattern.
// The pattern's elements can also have a smaller element_id than elements committed before the export (the sequence
// does not order commits).
func TestArchiveLatePatternReachesDims(t *testing.T) {
	ctx := context.Background()
	d := newTestData()
	for _, reserved := range []bool{false, true} {
		t.Run(map[bool]string{false: "pattern after the export", true: "element_id reserved before the export"}[reserved], func(t *testing.T) {
			opts, db := newTestNewDB(t, d, "2026-08", "2026-09")
			b := newFakeBucket()
			now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
			a := newTestArchiver(t, opts, db, b, &now, "2026-07")
			if _, err := CreatePartitions(ctx, db, now, time.Second); err != nil {
				t.Fatal(err)
			}
			// late is the transaction of the late report, which stores its new pattern's elements first
			late, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer late.Rollback() //nolint:errcheck
			const element = `INSERT INTO drop_pattern_elements (drop_pattern_id, item_id, quantity) VALUES ($1, 7, 2)`
			if reserved {
				// its element takes an element_id before another pattern's, which commits (and is exported) first
				if _, err := late.ExecContext(ctx, element, 1000); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, element, 1001); err != nil {
					t.Fatal(err)
				}
			}
			// the month's lock is held when the run gets to it, after the drop pattern elements were exported: the
			// report comes then
			unlock, err := lockTarget(a.opts.Dir, MonthTarget("2026-08"))
			if err != nil {
				t.Fatal(err)
			}
			inserted := errors.New("the run did not wait for the month's lock")
			testHookLockWait = func(target string) {
				if target != MonthTarget("2026-08") {
					return
				}
				testHookLockWait = nil
				defer unlock()
				if !reserved {
					if _, inserted = late.ExecContext(ctx, element, 1000); inserted != nil {
						return
					}
				}
				if _, inserted = late.ExecContext(ctx, `INSERT INTO drop_reports (report_id, stage_id, pattern_id, times, created_at, reliability, server, account_id)
					VALUES (9000001, 1, 1000, 1, '2026-08-15', 0, 'CN', 1)`); inserted != nil {
					return
				}
				inserted = late.Commit()
			}
			t.Cleanup(func() { testHookLockWait = nil })
			verified := 0
			setArchiveHook(t, func(month, step string) error {
				if month == "2026-08" && step == "verified" {
					verified++
				}
				return nil
			})

			res, err := a.Run(ctx)
			if inserted != nil {
				t.Fatal(inserted)
			}
			if err != nil || strings.Join(res.Pending, " ") != "2026-08" {
				t.Fatalf("run = %+v, %v", res, err)
			}
			if verified != 2 {
				t.Errorf("2026-08 verified %d times, want 2 (the try with the stale drop pattern elements, then the next)", verified)
			}
			r := reader(t)
			if got := queryStrings(t, r, `SELECT count(*)::VARCHAR FROM read_parquet([`+
				quote(filepath.Join(a.opts.Dir, DropPatternElementsFile))+`]) WHERE drop_pattern_id = 1000`); got[0] != "1" {
				t.Errorf("elements of the new pattern in the published drop pattern elements: %s", got[0])
			}
			if got := queryStrings(t, r, `SELECT count(*)::VARCHAR FROM read_parquet([`+
				quote(filepath.Join(a.opts.Dir, FilePath(KindDropReports, "2026-08")))+`]) WHERE pattern_id = 1000`); got[0] != "1" {
				t.Errorf("reports of the new pattern in the month's file: %s", got[0])
			}
		})
	}
}

// A late writer of the month persists a report, then its extra, in one transaction (as reportwkr does): the
// archiving locks the month's partitions in the same order, so that it waits for the writer instead of deadlocking
// with it (it would hold the extras' partition while waiting for the reports' one the writer holds).
func TestArchiveLocksReportsBeforeExtras(t *testing.T) {
	ctx := context.Background()
	d := newTestData()
	opts, db := newTestNewDB(t, d, "2026-08", "2026-09")
	b := newFakeBucket()
	now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	a := newTestArchiver(t, opts, db, b, &now, "2026-07")
	a.opts.LockTimeout = 5 * time.Second
	// the writer's deadlock check (deadlock_timeout 1s) runs first: a deadlock fails the writer
	a.db = sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(opts.PostgresDSN + "&deadlock_timeout=60s")))
	t.Cleanup(func() { a.db.Close() })
	if _, err := CreatePartitions(ctx, db, now, time.Second); err != nil {
		t.Fatal(err)
	}

	writer, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback() //nolint:errcheck
	if _, err := writer.ExecContext(ctx, `INSERT INTO drop_reports (report_id, stage_id, pattern_id, times, created_at, reliability, server, account_id)
		VALUES (9000002, 1, 1, 1, '2026-08-20', 0, 'CN', 1)`); err != nil {
		t.Fatal(err)
	}
	written := make(chan error, 1)
	go func() {
		// the archiving waits for the reports' partition, then the writer stores the extra and gives up
		if err := waitForLockWait(ctx, db, "drop_reports_2026_08", "ShareLock"); err != nil {
			written <- err
			return
		}
		_, err := writer.ExecContext(ctx, `INSERT INTO drop_report_extras (report_id, ip, created_at) VALUES (9000002, '192.0.2.0/24', '2026-08-20')`)
		writer.Rollback() //nolint:errcheck
		written <- err
	}()

	res, err := a.Run(ctx)
	if werr := <-written; werr != nil {
		t.Fatalf("writer: %v", werr)
	}
	if err != nil || strings.Join(res.Pending, " ") != "2026-08" {
		t.Fatalf("run = %+v, %v", res, err)
	}
}

// pg_locks lists the locks of every database of the cluster, where relation OIDs are unique only within one: a query
// that waits in another database (a copy of the archived one) for the relation of a partition's OID does not hold up
// the drop.
func TestArchiveDropIgnoresOtherDatabases(t *testing.T) {
	ctx := context.Background()
	dsn, extDir := os.Getenv("PENGUIN_TEST_POSTGRES_DSN"), os.Getenv("PENGUIN_TEST_DUCKDB_EXTENSION_DIR")
	if dsn == "" || extDir == "" {
		t.Skip("PENGUIN_TEST_POSTGRES_DSN or PENGUIN_TEST_DUCKDB_EXTENSION_DIR is not set")
	}
	admin := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
	t.Cleanup(func() { admin.Close() })
	dbDSN := func(name string) string {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		u.Path = "/" + name
		return u.String()
	}
	createDB := func(name, template string) {
		if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name+" TEMPLATE "+template); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := admin.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
				t.Error(err)
			}
		})
	}
	archived := fmt.Sprintf("it_%d_%d", time.Now().UnixNano(), rand.Int63())
	other := archived + "_copy"
	createDB(archived, "template0")
	db := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dbDSN(archived))))
	loadNewDB(t, db, newTestData(), "2026-08", "2026-09")
	// a database is copied only while nobody is connected to it
	db.Close()
	createDB(other, archived)
	db = sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dbDSN(archived))))
	t.Cleanup(func() { db.Close() })
	odb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dbDSN(other))))
	t.Cleanup(func() { odb.Close() })
	partOID := func(db *sql.DB) (oid int64) {
		if err := db.QueryRowContext(ctx, `SELECT 'drop_reports_2026_08'::regclass::oid`).Scan(&oid); err != nil {
			t.Fatal(err)
		}
		return oid
	}
	oid := partOID(db)
	if partOID(odb) != oid {
		t.Fatal("the copy's partition has another OID")
	}

	opts := Options{PostgresDSN: dbDSN(archived), ExtensionDirectory: extDir, WorkDir: t.TempDir(), Threads: 2, MemoryLimit: "1GB"}
	b := newFakeBucket()
	now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	a := newTestArchiver(t, opts, db, b, &now, "2026-07")
	if res, err := a.Run(ctx); err != nil || strings.Join(res.Pending, " ") != "2026-08" {
		t.Fatalf("run that records the month = %+v, %v", res, err)
	}
	now = now.Add(DefaultDropDelay)

	// in the copy, a query waits for the partition of the same OID
	holder, err := odb.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback() //nolint:errcheck
	if _, err := holder.ExecContext(ctx, `LOCK TABLE drop_reports_2026_08 IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := odb.ExecContext(ctx, `SELECT count(*) FROM drop_reports_2026_08`)
		done <- err
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var waiting bool
		if err := admin.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM pg_locks WHERE NOT granted AND relation = $1
			AND database = (SELECT oid FROM pg_database WHERE datname = $2))`, oid, other).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the query in the copy does not wait for the partition")
		}
	}

	res, err := a.Run(ctx)
	if err != nil || strings.Join(res.Archived, " ") != "2026-08" {
		t.Errorf("run = %+v, %v", res, err)
	}
	holder.Rollback() //nolint:errcheck
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestBoundMonth(t *testing.T) {
	for bound, want := range map[string]string{
		"FOR VALUES FROM ('2026-08-01 00:00:00+00') TO ('2026-09-01 00:00:00+00')":       "2026-08",
		"FOR VALUES FROM ('2026-08-01 08:00:00+08') TO ('2026-09-01 08:00:00+08')":       "2026-08",
		"FOR VALUES FROM ('2026-07-31 19:30:00-04:30') TO ('2026-08-31 19:30:00-04:30')": "2026-08",
		"FOR VALUES FROM ('2026-12-01 00:00:00+00') TO ('2027-01-01 00:00:00+00')":       "2026-12",
		"FOR VALUES FROM ('2026-08-01 00:00:00+00') TO ('2026-08-15 00:00:00+00')":       "",
		"FOR VALUES FROM ('2026-08-02 00:00:00+00') TO ('2026-09-01 00:00:00+00')":       "",
		"FOR VALUES FROM ('2019-04-01 00:00:00+00') TO ('2019-05-01 00:00:00+00')":       "",
		"FOR VALUES FROM (MINVALUE) TO ('2026-09-01 00:00:00+00')":                       "",
		"DEFAULT": "",
	} {
		m, ok := boundMonth(bound)
		if (want == "") == ok || (ok && m.Key != want) {
			t.Errorf("boundMonth(%q) = %v, %v; want %q", bound, m, ok, want)
		}
	}
}
