package coldstore

import (
	"context"
	"strconv"
	"testing"
	"time"
)

const (
	// statsTestReports and statsTestExtras are relations of reportColumns and extrasColumns (7 reports, recruit 42
	// included, and 5 extras).
	statsTestReports = `SELECT * FROM (VALUES
		(1::BIGINT, 10::INTEGER, 100::BIGINT, 1::INTEGER, TIMESTAMPTZ '2023-10-01 00:00:00.045203+00', 0::INTEGER, 'CN', 7::BIGINT, 'MeoAssistant', 'v4.24.0'),
		(2, 10, 101, 2, TIMESTAMPTZ '2023-10-05 12:30:00+00', 0, 'US', NULL, 'penguin-stats.io', NULL),
		(3, 11, 102, 6, TIMESTAMPTZ '2023-10-09 08:00:00+00', 1, 'JP', 9, NULL, 'v1'),
		(4, 42, 103, 1, TIMESTAMPTZ '2023-10-12 23:59:59.999999+00', 0, 'CN', 7, 'MeoAssistant', 'v4.24.0'),
		(5, 12, 104, 3, TIMESTAMPTZ '2023-10-20 01:02:03+00', -1, 'KR', 11, 'frontend-v2', 'v2'),
		(6, 42, 105, 1, TIMESTAMPTZ '2023-10-25 06:00:00+00', 1, 'CN', NULL, NULL, NULL),
		(7, 10, 100, 1, TIMESTAMPTZ '2023-10-31 23:59:59.955393+00', 0, 'CN', 7, 'MeoAssistant', 'v4.25.0')
	) AS t(` + reportColumns + `)`
	statsTestExtras = `SELECT * FROM (VALUES
		(1::BIGINT, '1.2.3.4', '{"a":1}', 'd41d8cd98f00b204e9800998ecf8427e'),
		(2, '2001:db8::1', NULL, NULL),
		(3, '5.6.7.8', '{}', '0cc175b9c0f1b6a831c399e269772661'),
		(4, '1.2.3.4', '{"recruit":true}', NULL),
		(7, '9.9.9.9', '{"a":2}', '92eb5ffee6ae2fec3ad71c777531578f')
	) AS t(` + extrasColumns + `)`
)

// TestMonthStatsValues pins the values monthStats computes: the manifests of the archived months hold them, and
// their verification compares them as they are, so a change of the query (or of DuckDB's hash) must not change them.
func TestMonthStatsValues(t *testing.T) {
	ctx := context.Background()
	j, err := newJob(ctx, Options{WorkDir: t.TempDir(), Threads: 2, MemoryLimit: "256MB"}, "test-stats", false)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	got, err := j.monthStats(ctx, statsTestReports, statsTestExtras, 42)
	if err != nil {
		t.Fatal(err)
	}
	minID, maxID := int64(1), int64(7)
	minTime, maxTime := mustTime(t, "2023-10-01T00:00:00.045203Z"), mustTime(t, "2023-10-31T23:59:59.955393Z")
	want := MonthStats{
		Rows: 7, RecruitRows: 2, ExtrasRows: 5, ReliableRows: 3, ReliableTimes: 4, SumTimes: 15,
		MinReportID: &minID, MaxReportID: &maxID, MinCreatedAt: &minTime, MaxCreatedAt: &maxTime,
		ReportsHash: "75962520230029317918",
		ExtrasHash:  "55106109766607733013",
	}
	if p := want.diff(got); len(p) > 0 {
		t.Errorf("stats: %v", p)
	}

	// no rows: zero counts and hashes, no ranges
	got, err = j.monthStats(ctx, statsTestReports+" WHERE false", statsTestExtras+" WHERE false", 42)
	if err != nil {
		t.Fatal(err)
	}
	if p := (MonthStats{ReportsHash: "0", ExtrasHash: "0"}).diff(got); len(p) > 0 {
		t.Errorf("empty stats: %v", p)
	}
}

// TestMonthStatsStreams checks that monthStats reads the extras without holding them: a CTE referenced twice is
// materialized, which spilled the extras of the largest month (2023-10) to the temp directory until it overflowed.
// Here the extras are several times the memory limit, and the job may not spill at all.
func TestMonthStatsStreams(t *testing.T) {
	ctx := context.Background()
	j, err := newJob(ctx, Options{WorkDir: t.TempDir(), Threads: 2, MemoryLimit: "64MB"}, "test-stats-streams", false)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if err := j.exec(ctx, "SET max_temp_directory_size = '0B'"); err != nil {
		t.Fatal(err)
	}
	const n = 1_000_000
	reports := `SELECT range AS report_id, 10::INTEGER AS stage_id, range AS pattern_id, 1::INTEGER AS times,
		TIMESTAMPTZ '2023-10-01 00:00:00+00' AS created_at, 0::INTEGER AS reliability, 'CN' AS server,
		range AS account_id, 'MeoAssistant' AS source_name, 'v1' AS version FROM range(` + strconv.Itoa(n) + `)`
	// 1M distinct strings of ~300 bytes: ~300 MB of extras
	extras := `SELECT range AS report_id, range::VARCHAR AS ip, repeat('m', 280) || range::VARCHAR AS metadata,
		md5(range::VARCHAR) AS md5 FROM range(` + strconv.Itoa(n) + `)`
	got, err := j.monthStats(ctx, reports, extras, 42)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows != n || got.ExtrasRows != n || got.ReliableRows != n {
		t.Errorf("stats = %+v", got)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
