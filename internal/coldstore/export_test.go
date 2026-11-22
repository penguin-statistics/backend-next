package coldstore

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
)

// The helpers of archive_integration_test.go used by archive_recall_integration_test.go (package coldstore_test).
var (
	NewTestData     = newTestData
	NewTestNewDB    = newTestNewDB
	NewFakeBucket   = newFakeBucket
	NewTestArchiver = newTestArchiver
	SetArchiveHook  = setArchiveHook
)

// loadSkewedMonth fills the tables r and e (as loadSource does) with n reports of 2026-08 shaped like that month in
// PostgreSQL: 77% recruit reports, every report with extras, NULL metadata and md5.
func loadSkewedMonth(t *testing.T, j *job, n int) {
	t.Helper()
	ctx := context.Background()
	for _, s := range []string{
		fmt.Sprintf(`CREATE TABLE r AS SELECT i::BIGINT AS report_id,
				(CASE WHEN i %% 100 < 77 THEN %d ELSE 1 + i %% 300 END)::INTEGER AS stage_id,
				(i %% 5000)::BIGINT AS pattern_id, (1 + i %% 3)::INTEGER AS times,
				TIMESTAMPTZ '2026-08-01 00:00:00+00' + to_microseconds((i * 7919) %% 2678400000000) AS created_at,
				(i %% 7 = 0)::INTEGER AS reliability, ['CN', 'US', 'JP', 'KR'][1 + i %% 4] AS server,
				(hash(i) %% 350000)::BIGINT AS account_id, 'MeoAssistant-' || (i %% 13) AS source_name,
				'v5.' || (i %% 50) AS version
			FROM range(%d) t(i)`, testRecruitStageID, n),
		`CREATE TABLE e AS SELECT report_id, md5(report_id::VARCHAR)[1:15] || ':' || (report_id % 997) AS ip,
			NULL::VARCHAR AS metadata, NULL::VARCHAR AS md5 FROM r`,
	} {
		if err := j.exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
}

// TestWriteMonthWithinMemoryLimit exports a month that is mostly recruit reports under a small memory limit. The
// recruit file's join and sort in one statement ran out of memory here at every try, at 64, 96 and 128 MB (as
// 2026-08 did in production: "Out of Memory Error: failed to pin block of size 256.0 KiB"); apart, they pass from
// 64 MB.
func TestWriteMonthWithinMemoryLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("exports 2M reports")
	}
	ctx := context.Background()
	j, err := newJob(ctx, Options{WorkDir: t.TempDir(), Threads: 2, MemoryLimit: "96MB"}, "test-memory", false)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	const n = 2_000_000
	loadSkewedMonth(t, j, n)
	e, err := j.writeMonth(ctx, mustMonth(t, "2026-08"), t.TempDir(), testRecruitStageID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.Rows != n || e.RecruitRows != n*77/100 || e.ExtrasRows != n {
		t.Errorf("rows %d, recruit %d, extras %d", e.Rows, e.RecruitRows, e.ExtrasRows)
	}
}

// TestJoinedFilesMatchSingleStatement checks that the recruit and extras files, joined and sorted in statements of
// their own, are byte for byte the files of the single COPYs that joined and sorted at once, and that the joins'
// tables are gone.
func TestJoinedFilesMatchSingleStatement(t *testing.T) {
	ctx := context.Background()
	m := mustMonth(t, "2026-08")
	d := newTestData()
	j, err := newJob(ctx, Options{WorkDir: t.TempDir(), Threads: 2, MemoryLimit: "512MB"}, "test-recruit", false)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	for _, s := range append([]string{
		`CREATE TABLE r (report_id BIGINT, stage_id INTEGER, pattern_id BIGINT, times INTEGER, created_at TIMESTAMPTZ,
			reliability INTEGER, server VARCHAR, account_id BIGINT, source_name VARCHAR, version VARCHAR)`,
		`CREATE TABLE e (report_id BIGINT, ip VARCHAR, metadata VARCHAR, md5 VARCHAR)`,
	}, d.insertSQL("r", "e", d.inMonth(m))...) {
		if err := j.exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	entry, err := j.writeMonth(ctx, m, dir, testRecruitStageID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if entry.RecruitRows < 100 {
		t.Fatalf("recruit rows = %d", entry.RecruitRows)
	}
	for _, table := range []string{recruitJoinedTable, extrasJoinedTable} {
		var left int64
		if err := j.queryRow(ctx, `SELECT count(*) FROM duckdb_tables() WHERE table_name = `+quote(table), &left); err != nil {
			t.Fatal(err)
		}
		if left != 0 {
			t.Errorf("table %s left in the job's database", table)
		}
	}

	recruit := strconv.Itoa(testRecruitStageID)
	for _, c := range []struct {
		kind Kind
		copy string
	}{
		{KindRecruit, `SELECT r.* EXCLUDE (stage_id), e.ip, e.metadata, e.md5 FROM r LEFT JOIN e USING (report_id)
			WHERE r.stage_id = ` + recruit + ` ORDER BY r.created_at, r.report_id`,
		},
		{KindExtras, `SELECT e.* FROM e SEMI JOIN r ON r.report_id = e.report_id AND r.stage_id <> ` + recruit + `
			ORDER BY e.report_id`,
		},
	} {
		kv := "KV_METADATA {month: '2026-08', sort: 'created_at', stage_id: " + quote(recruit) + "}"
		if c.kind == KindExtras {
			kv = "KV_METADATA {month: '2026-08', sort: 'report_id'}"
		}
		single := filepath.Join(t.TempDir(), "single.parquet")
		if err := j.exec(ctx, `COPY (`+c.copy+`) TO `+quote(single)+` (`+parquetOptions+`, `+kv+`)`); err != nil {
			t.Fatal(err)
		}
		want, err := describeFile(single)
		if err != nil {
			t.Fatal(err)
		}
		if got := entry.File(c.kind); got.SHA256 != want.SHA256 || got.Bytes != want.Bytes {
			t.Errorf("%s file %s (%d bytes), the single statement's %s (%d bytes)", c.kind, got.SHA256, got.Bytes, want.SHA256, want.Bytes)
		}
	}
}
