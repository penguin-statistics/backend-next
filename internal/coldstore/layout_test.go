package coldstore

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseMonth(t *testing.T) {
	m, err := ParseMonth("2026-09")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Start.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) || !m.End.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("2026-09 = [%v, %v)", m.Start, m.End)
	}
	m, err = ParseMonth("2026-12")
	if err != nil || m.End.Format("2006-01") != "2027-01" {
		t.Errorf("2026-12 ends %v, %v", m.End, err)
	}
	m, err = ParseMonth("pre")
	if err != nil || !m.IsPre() || !m.Start.IsZero() || !m.End.Equal(Epoch) {
		t.Errorf("pre = %+v, %v", m, err)
	}
	for _, bad := range []string{"", "2026-9", "2026-13", "2019-04", "2026-09-01", "PRE", "2026/09"} {
		if _, err := ParseMonth(bad); err == nil {
			t.Errorf("ParseMonth(%q) succeeded", bad)
		}
	}
}

func TestMonthOf(t *testing.T) {
	cases := map[time.Time]string{
		time.Unix(0, 0): "pre",
		time.Date(2019, 4, 30, 23, 59, 59, 999999000, time.UTC): "pre",
		Epoch: "2019-05",
		time.Date(2026, 8, 31, 23, 59, 59, 999999000, time.UTC): "2026-08",
		// 2026-09-01 07:00 in UTC+8 is still 2026-08-31 in UTC: months are UTC months
		time.Date(2026, 9, 1, 7, 0, 0, 0, time.FixedZone("UTC+8", 8*3600)): "2026-08",
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC):                        "2026-09",
	}
	for at, want := range cases {
		m := MonthOf(at)
		if m.Key != want {
			t.Errorf("MonthOf(%v) = %s, want %s", at, m.Key, want)
		}
		if !m.IsPre() && (at.Before(m.Start) || !at.Before(m.End)) {
			t.Errorf("MonthOf(%v) = [%v, %v)", at, m.Start, m.End)
		}
	}
}

func TestMonthOver(t *testing.T) {
	m := mustMonth(t, "2026-10")
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		now   time.Time
		grace time.Duration
		want  bool
	}{
		{end.Add(-time.Microsecond), 0, false},
		{end, 0, true},
		// reports created at the month's last minute may still be in the queue
		{end.Add(5 * time.Minute), DefaultGrace, false},
		{end.Add(DefaultGrace - time.Second), DefaultGrace, false},
		{end.Add(DefaultGrace), DefaultGrace, true},
	}
	for _, c := range cases {
		if got := m.Over(c.now, c.grace); got != c.want {
			t.Errorf("Over(%v, %v) = %v, want %v", c.now, c.grace, got, c.want)
		}
	}
	if !mustMonth(t, PreMonth).Over(Epoch.Add(DefaultGrace), DefaultGrace) {
		t.Error("pre is not over")
	}
}

func TestFilePath(t *testing.T) {
	want := map[Kind]string{
		KindDropReports: "drop_reports/drop_reports_2026-09.parquet",
		KindExtras:      "drop_report_extras/extras_2026-09.parquet",
		KindRecruit:     "recruit/recruit_2026-09.parquet",
		KindRollup:      "rollup/rollup_2026-09.parquet",
	}
	for kind, p := range want {
		if got := FilePath(kind, "2026-09"); got != p {
			t.Errorf("FilePath(%s) = %s, want %s", kind, got, p)
		}
		if key, ok := monthOfFile(kind, filepath.Base(p)); !ok || key != "2026-09" {
			t.Errorf("monthOfFile(%s) = %q, %v", p, key, ok)
		}
	}
	if key, ok := monthOfFile(KindDropReports, "drop_reports_pre.parquet"); !ok || key != "pre" {
		t.Errorf("monthOfFile(pre) = %q, %v", key, ok)
	}
	for _, name := range []string{"drop_reports_2026-09.parquet.0a1b.tmp", "extras_2026-09.parquet", "drop_reports_.parquet", "drop_reports_x.parquet"} {
		if _, ok := monthOfFile(KindDropReports, name); ok {
			t.Errorf("monthOfFile(%s) succeeded", name)
		}
	}
}

func TestLibpqDSN(t *testing.T) {
	cases := map[string]string{
		"postgres://u:p%40ss@127.0.0.1:5434/db?sslmode=disable&dial_timeout=10s&read_timeout=1200s&write_timeout=120s": "postgres://u:p%40ss@127.0.0.1:5434/db?connect_timeout=10&sslmode=disable",
		"postgresql://u@h/db": "postgresql://u@h/db",
		"postgres://u@h/db?search_path=it 1&application_name=x": "postgres://u@h/db?application_name=x&options=-c%20search_path%3Dit%5C%201",
		"postgres://u@h/db?connect_timeout=1500ms&timeout=5s":   "postgres://u@h/db?connect_timeout=2",
		"postgres://u@h/db?statement_timeout=0&search_path=a,b": "postgres://u@h/db?options=-c%20search_path%3Da%2Cb%20-c%20statement_timeout%3D0",
	}
	for in, want := range cases {
		got, err := libpqDSN(in)
		if err != nil || got != want {
			t.Errorf("libpqDSN(%s) = %s, %v; want %s", in, got, err, want)
		}
	}
	for _, bad := range []string{"unix:///tmp/.s.PGSQL.5432", "mysql://h/db", "postgres://h/db?dial_timeout=soon"} {
		if _, err := libpqDSN(bad); err == nil {
			t.Errorf("libpqDSN(%s) succeeded", bad)
		}
	}
}

// testPasswords are passwords whose encodings differ: in the userinfo of a URI, in a query and decoded.
var testPasswords = []string{"two words", "s3cr/t", "p@ss:w?rd#1", "a+b c&d=e%f", `q"u'o\te`, "ünï cødé"}

// passwordForms returns the forms a password can take in an error message.
func passwordForms(p string) []string {
	q := url.QueryEscape(p)
	_, enc, _ := strings.Cut(url.UserPassword("u", p).String(), ":")
	return []string{p, q, strings.ReplaceAll(q, "+", "%20"), url.PathEscape(p), enc}
}

func TestRedactDSN(t *testing.T) {
	dsn := "postgres://u:s3cr%2Ft@h/db"
	if got := redactDSN("failed to connect to postgres://u:s3cr/t@h/db and s3cr%2Ft", dsn); got != "failed to connect to postgres://u:***@h/db and ***" {
		t.Errorf("redactDSN = %s", got)
	}

	for _, p := range testPasswords {
		// the DSN as the app's configuration may write it, and as libpqDSN rewrites it
		written := "postgres://runner:" + strings.ReplaceAll(url.QueryEscape(p), "+", "%20") + "@127.0.0.1:1/postgres?sslmode=disable"
		dsn, err := libpqDSN(written)
		if err != nil {
			t.Fatal(err)
		}
		var msg strings.Builder
		msg.WriteString(`IO Error: Unable to connect to Postgres at "` + dsn + `": ` + written)
		for _, f := range passwordForms(p) {
			msg.WriteString(" | postgres://runner:" + f + "@127.0.0.1:1/postgres | " + f)
		}
		got := redactDSN(msg.String(), dsn, written)
		for _, f := range passwordForms(p) {
			if strings.Contains(got, f) {
				t.Errorf("password %q: %q left in %s", p, f, got)
			}
		}
		if !strings.Contains(got, "postgres://runner:***@127.0.0.1:1/postgres") {
			t.Errorf("password %q: %s", p, got)
		}
	}
	if got := redactDSN("no password here postgres://u@h/db", "postgres://u@h/db"); got != "no password here postgres://u@h/db" {
		t.Errorf("redactDSN without a password = %s", got)
	}
}

func TestManifestPutAndRoundTrip(t *testing.T) {
	m := NewManifest()
	for _, key := range []string{"2026-09", "pre", "2019-05", "2026-08"} {
		m.PutMonth(MonthEntry{Month: key})
	}
	m.PutMonth(MonthEntry{Month: "2026-08", RecruitStageID: 7})
	var keys []string
	for _, e := range m.Months {
		keys = append(keys, e.Month)
	}
	if got := len(keys); got != 4 || keys[0] != "pre" || keys[1] != "2019-05" || keys[2] != "2026-08" || keys[3] != "2026-09" {
		t.Errorf("months = %v", keys)
	}
	if m.Month("2026-08").RecruitStageID != 7 {
		t.Error("PutMonth did not replace 2026-08")
	}

	name := filepath.Join(t.TempDir(), "sub", ManifestFile)
	if err := WriteManifest(name, m); err != nil {
		t.Fatal(err)
	}
	back, err := ReadManifest(name)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Months) != 4 || back.Layout != Prefix || back.SchemaVersion != SchemaVersion {
		t.Errorf("read back %+v", back)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(name), "*.tmp")); len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}

	if err := os.WriteFile(name, []byte(`{"schema_version": 2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(name); err == nil {
		t.Error("ReadManifest accepted schema version 2")
	}
}

func TestUpdateManifestConcurrently(t *testing.T) {
	name := filepath.Join(t.TempDir(), ManifestFile)
	months := []string{"2026-01", "2026-02", "2026-03", "2026-04", "2026-05", "2026-06", "2026-07", "2026-08"}
	var wg sync.WaitGroup
	for _, key := range months {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := UpdateManifest(name, func(m *Manifest) error {
				m.PutMonth(MonthEntry{Month: key})
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	m, err := ReadManifest(name)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Months) != len(months) {
		t.Errorf("%d months recorded, want %d: an update was lost", len(m.Months), len(months))
	}

	boom := errors.New("boom")
	if err := UpdateManifest(name, func(m *Manifest) error { m.Months = nil; return boom }); !errors.Is(err, boom) {
		t.Errorf("UpdateManifest = %v, want boom", err)
	}
	if m, _ := ReadManifest(name); len(m.Months) != len(months) {
		t.Error("a failed update was written")
	}
}
