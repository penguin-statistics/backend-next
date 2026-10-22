package coldstore

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/rand"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"exusiai.dev/backend-next/internal/pkg/duckdbx"
)

// testRecruitStageID is deliberately not production's 788: the exporter has to look the recruit stage up.
const testRecruitStageID = 42

type testReport struct {
	id          int64
	stage       int
	pattern     int64
	times       int
	createdAt   time.Time
	reliability int
	server      string
	account     int64
	source      *string
	version     *string
}

type testExtra struct {
	id       int64
	ip       string
	metadata *string
	md5      *string
}

type testData struct {
	reports []testReport
	extras  []testExtra
}

func strp(s string) *string { return &s }

// newTestData generates reports in 2026-08, 2026-09 and before the epoch, on every server, with recruit reports,
// manual and automated sources, unreliable reports, reports sharing an account and created_at (as the reports of
// one batch do), reports without extras, extras with NULL columns and an orphan extras row inside the months'
// report_id range.
func newTestData() *testData {
	rnd := rand.New(rand.NewSource(752))
	d := &testData{}
	servers := []string{"CN", "US", "JP", "KR"}
	sources := []*string{strp("frontend-v2"), strp("MeoAssistant"), strp("penguin-stats.io"), nil}
	months := []time.Time{time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}

	const n = 3000
	const orphanID = 1500
	for i := int64(1); i <= n; i++ {
		if i == orphanID {
			// the report was deleted, its extras were not
			d.extras = append(d.extras, testExtra{id: i, ip: "192.0.2.0/24"})
			continue
		}
		r := testReport{
			id:          i,
			stage:       []int{1, 2, testRecruitStageID, testRecruitStageID, 3}[rnd.Intn(5)],
			pattern:     int64(1 + rnd.Intn(20)),
			times:       1 + rnd.Intn(6)*rnd.Intn(2),
			reliability: []int{0, 0, 0, 0, 1, 2}[rnd.Intn(6)],
			server:      servers[rnd.Intn(len(servers))],
			account:     int64(1 + rnd.Intn(30)),
			source:      sources[rnd.Intn(len(sources))],
		}
		if rnd.Intn(3) > 0 {
			r.version = strp("v3." + strconv.Itoa(rnd.Intn(5)) + ".0")
		}
		start := months[rnd.Intn(2)]
		r.createdAt = start.Add(time.Duration(rnd.Int63n(int64(start.AddDate(0, 1, 0).Sub(start))))).Truncate(time.Microsecond)
		if prev := len(d.reports) - 1; prev >= 0 && i%25 == 0 {
			// a batch: same account, server and created_at as the previous report
			p := d.reports[prev]
			r.account, r.server, r.createdAt = p.account, p.server, p.createdAt
		}
		switch i {
		case 7:
			r.createdAt = time.Unix(0, 0).UTC()
		case 8:
			r.createdAt = Epoch.Add(-time.Second)
		case 9:
			r.createdAt = months[1]
		case 10:
			r.createdAt = months[1].Add(-time.Microsecond)
		}
		d.reports = append(d.reports, r)

		if i%13 == 0 {
			continue // no extras
		}
		e := testExtra{id: i, ip: fmt.Sprintf("10.%d.%d.0/24", i%250, i%7)}
		if i%4 == 0 {
			e.ip = fmt.Sprintf("2001:db8:%x::/48", i)
		}
		if i%3 != 0 {
			e.metadata = strp(fmt.Sprintf(`{"n": %d}`, i))
		}
		if i%5 != 0 {
			sum := md5.Sum([]byte(strconv.FormatInt(i, 10)))
			e.md5 = strp(hex.EncodeToString(sum[:]))
		}
		d.extras = append(d.extras, e)
	}
	return d
}

func sqlNullable(s *string) string {
	if s == nil {
		return "NULL"
	}
	return quote(*s)
}

func sqlTime(t time.Time) string {
	return quote(t.UTC().Format("2006-01-02 15:04:05.999999") + "+00")
}

// insertSQL returns INSERT statements of the reports and extras, for PostgreSQL or DuckDB.
func (d *testData) insertSQL(reportsTable, extrasTable string, keep func(testReport) bool) []string {
	var rows []string
	ids := map[int64]bool{}
	for _, r := range d.reports {
		if keep != nil && !keep(r) {
			continue
		}
		ids[r.id] = true
		rows = append(rows, fmt.Sprintf("(%d, %d, %d, %d, %s, %d, %s, %d, %s, %s)", r.id, r.stage, r.pattern, r.times,
			sqlTime(r.createdAt), r.reliability, quote(r.server), r.account, sqlNullable(r.source), sqlNullable(r.version)))
	}
	var out []string
	if len(rows) > 0 {
		out = append(out, "INSERT INTO "+reportsTable+" ("+reportColumns+") VALUES "+strings.Join(rows, ", "))
	}
	rows = nil
	for _, e := range d.extras {
		if keep != nil && !ids[e.id] {
			continue
		}
		rows = append(rows, fmt.Sprintf("(%d, %s, %s, %s)", e.id, quote(e.ip), sqlNullable(e.metadata), sqlNullable(e.md5)))
	}
	if len(rows) > 0 {
		out = append(out, "INSERT INTO "+extrasTable+" ("+extrasColumns+") VALUES "+strings.Join(rows, ", "))
	}
	return out
}

func (d *testData) inMonth(m Month) func(testReport) bool {
	return func(r testReport) bool {
		return MonthOf(r.createdAt).Key == m.Key
	}
}

// expected computes what the manifest entry of a month must say, but the content hashes.
func (d *testData) expected(m Month) MonthStats {
	var s MonthStats
	extras := map[int64]bool{}
	for _, e := range d.extras {
		extras[e.id] = true
	}
	for _, r := range d.reports {
		if !d.inMonth(m)(r) {
			continue
		}
		s.Rows++
		s.SumTimes += int64(r.times)
		if r.stage == testRecruitStageID {
			s.RecruitRows++
		} else if r.reliability == 0 {
			s.ReliableRows++
			s.ReliableTimes += int64(r.times)
		}
		if extras[r.id] {
			s.ExtrasRows++
		}
		id, at := r.id, r.createdAt
		if s.MinReportID == nil || id < *s.MinReportID {
			s.MinReportID = &id
		}
		if s.MaxReportID == nil || id > *s.MaxReportID {
			s.MaxReportID = &id
		}
		if s.MinCreatedAt == nil || at.Before(*s.MinCreatedAt) {
			s.MinCreatedAt = &at
		}
		if s.MaxCreatedAt == nil || at.After(*s.MaxCreatedAt) {
			s.MaxCreatedAt = &at
		}
	}
	return s
}

// expectedRollup returns the rollup rows of a month as sorted "server,stage,pattern,manual,one_time,hour,cnt,times"
// strings.
func (d *testData) expectedRollup(m Month) []string {
	type key struct {
		server          string
		stage           int
		pattern         int64
		manual, oneTime bool
		hour            int64
	}
	sums := map[key][2]int64{}
	for _, r := range d.reports {
		if !d.inMonth(m)(r) || r.stage == testRecruitStageID || r.reliability != 0 {
			continue
		}
		manual := r.source != nil && (*r.source == "frontend-v2" || *r.source == "penguin-stats.io" || *r.source == "penguin-stats.io(internal)")
		k := key{r.server, r.stage, r.pattern, manual, r.times == 1, r.createdAt.Truncate(time.Hour).Unix()}
		v := sums[k]
		sums[k] = [2]int64{v[0] + 1, v[1] + int64(r.times)}
	}
	var out []string
	for k, v := range sums {
		out = append(out, fmt.Sprintf("%s,%d,%d,%t,%t,%d,%d,%d", k.server, k.stage, k.pattern, k.manual, k.oneTime, k.hour, v[0], v[1]))
	}
	sort.Strings(out)
	return out
}

// reader opens an in-memory DuckDB to read files in assertions.
func reader(t *testing.T) *sql.DB {
	t.Helper()
	db, err := duckdbx.Open(duckdbx.Config{Threads: 2, TempDirectory: filepath.Join(t.TempDir(), "tmp")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("SET TimeZone = 'UTC'"); err != nil {
		t.Fatal(err)
	}
	return db
}

func queryStrings(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// checkMonthFiles checks a month's files and manifest entry against the test data.
func checkMonthFiles(t *testing.T, d *testData, dir string, e *MonthEntry, m Month) {
	t.Helper()
	want := d.expected(m)
	got := e.MonthStats
	want.ReportsHash, want.ExtrasHash = got.ReportsHash, got.ExtrasHash
	if p := want.diff(got); len(p) > 0 {
		t.Errorf("%s stats: %v", m.Key, p)
	}
	if e.RecruitStageID != testRecruitStageID {
		t.Errorf("%s recruit stage id = %d", m.Key, e.RecruitStageID)
	}
	if len(e.Files) != len(Kinds) {
		t.Fatalf("%s files = %+v", m.Key, e.Files)
	}

	db := reader(t)
	file := func(kind Kind) string {
		return "read_parquet([" + quote(filepath.Join(dir, FilePath(kind, m.Key))) + "], file_row_number = true)"
	}
	for _, kind := range Kinds {
		fe := e.File(kind)
		if fe == nil || fe.Bytes == 0 || len(fe.SHA256) != 64 {
			t.Errorf("%s %s entry = %+v", m.Key, kind, fe)
		}
		kv := strings.Join(queryStrings(t, db, `SELECT decode(key) || '=' || decode(value) FROM parquet_kv_metadata([`+
			quote(filepath.Join(dir, FilePath(kind, m.Key)))+`]) ORDER BY 1`), " ")
		wantKV := map[Kind]string{
			KindDropReports: "month=" + m.Key + " sort=server,account_id,created_at",
			KindExtras:      "month=" + m.Key + " sort=report_id",
			KindRecruit:     "month=" + m.Key + " sort=created_at stage_id=42",
			KindRollup:      "month=" + m.Key,
		}[kind]
		if kv != wantKV {
			t.Errorf("%s %s kv metadata = %q, want %q", m.Key, kind, kv, wantKV)
		}
		meta := queryStrings(t, db, `SELECT DISTINCT compression FROM parquet_metadata([`+quote(filepath.Join(dir, FilePath(kind, m.Key)))+`])`)
		if fe.Rows > 0 && (len(meta) != 1 || meta[0] != "ZSTD") {
			t.Errorf("%s %s compression = %v", m.Key, kind, meta)
		}
	}

	// rows and their order
	count := func(kind Kind, where string) int64 {
		var n int64
		if err := db.QueryRow("SELECT count(*) FROM " + file(kind) + " " + where).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(KindDropReports, ""); n != want.Rows-want.RecruitRows || n != e.File(KindDropReports).Rows {
		t.Errorf("%s drop_reports rows = %d, want %d", m.Key, n, want.Rows-want.RecruitRows)
	}
	if n := count(KindDropReports, "WHERE stage_id = 42"); n != 0 {
		t.Errorf("%s drop_reports has %d recruit rows", m.Key, n)
	}
	if n := count(KindRecruit, ""); n != want.RecruitRows {
		t.Errorf("%s recruit rows = %d, want %d", m.Key, n, want.RecruitRows)
	}
	unordered := map[Kind]string{
		KindDropReports: "(server, account_id, created_at, report_id)",
		KindExtras:      "(report_id)",
		KindRecruit:     "(created_at, report_id)",
		KindRollup:      "(hour, server, stage_id, pattern_id, is_manual, one_time)",
	}
	for kind, key := range unordered {
		// a row whose sort key is smaller than the previous row's
		q := `SELECT count(*) FROM (SELECT ` + key + ` AS k, lag(` + key + `) OVER (ORDER BY file_row_number) AS p
			FROM ` + file(kind) + `) WHERE k < p`
		var n int64
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if n != 0 {
			t.Errorf("%s %s: %d rows out of order", m.Key, kind, n)
		}
	}

	// extras: every non-recruit report with extras, never the orphan; recruit rows carry theirs or NULLs
	var extrasIDs, orphan, recruitWithIP, recruitNoIP int64
	if err := db.QueryRow(`SELECT count(*), count(*) FILTER (WHERE report_id = 1500) FROM `+file(KindExtras)+
		` x SEMI JOIN `+file(KindDropReports)+` r ON r.report_id = x.report_id`).Scan(&extrasIDs, &orphan); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(ip), count(*) - count(ip) FROM `+file(KindRecruit)).Scan(&recruitWithIP, &recruitNoIP); err != nil {
		t.Fatal(err)
	}
	if extrasIDs != count(KindExtras, "") || orphan != 0 || extrasIDs+recruitWithIP != want.ExtrasRows {
		t.Errorf("%s extras: %d rows (orphan %d) + %d in recruit, want %d", m.Key, extrasIDs, orphan, recruitWithIP, want.ExtrasRows)
	}
	if want.RecruitRows > 100 && recruitNoIP == 0 {
		t.Errorf("%s: no recruit report without extras", m.Key)
	}

	rollup := queryStrings(t, db, `SELECT concat_ws(',', server, stage_id, pattern_id, is_manual, one_time, epoch(hour)::BIGINT, cnt, times)
		FROM `+file(KindRollup)+` ORDER BY 1`)
	sort.Strings(rollup)
	if w := d.expectedRollup(m); strings.Join(rollup, "\n") != strings.Join(w, "\n") {
		t.Errorf("%s rollup:\n%s\nwant:\n%s", m.Key, strings.Join(rollup, "\n"), strings.Join(w, "\n"))
	}
}

// tamperFile rewrites a file of a month with one value changed, keeping its key-value metadata.
func tamperFile(t *testing.T, dir string, kind Kind, month, set string) {
	t.Helper()
	db := reader(t)
	name := filepath.Join(dir, FilePath(kind, month))
	var kv []string
	for _, pair := range queryStrings(t, db, `SELECT decode(key) || '=' || decode(value) FROM parquet_kv_metadata([`+quote(name)+`])`) {
		k, v, _ := strings.Cut(pair, "=")
		kv = append(kv, k+": "+quote(v))
	}
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE t AS SELECT * FROM read_parquet([`+quote(name)+`])`); err != nil {
		t.Fatal(err)
	}
	res, err := db.ExecContext(ctx, `UPDATE t SET `+set+` WHERE rowid = 0`)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("tamper %s: %d rows changed", name, n)
	}
	if _, err := db.ExecContext(ctx, `COPY t TO `+quote(name)+` (`+parquetOptions+`, KV_METADATA {`+strings.Join(kv, ", ")+`})`); err != nil {
		t.Fatal(err)
	}
}
