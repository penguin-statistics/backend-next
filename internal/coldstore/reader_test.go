package coldstore

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/errors"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// readerMonths are the months of newReaderTestData: the cold storage must cover every month from the epoch on.
var readerMonths = []string{"pre", "2019-05", "2019-06"}

// newReaderTestData is newTestData moved from 2026-08 and 2026-09 to 2019-05 and 2019-06 (same lengths), so that
// with "pre" its months cover the cold storage without a gap.
func newReaderTestData() *testData {
	d := newTestData()
	for i := range d.reports {
		switch MonthOf(d.reports[i].createdAt).Key {
		case "2026-08", "2026-09":
			d.reports[i].createdAt = d.reports[i].createdAt.AddDate(-7, -3, 0)
		}
	}
	return d
}

// writeReaderStore writes a complete cold storage directory of the months: their files, the drop pattern elements
// and the manifest.
func writeReaderStore(t *testing.T, d *testData, months ...string) (string, *Manifest) {
	t.Helper()
	dir := t.TempDir()
	m := NewManifest()
	for _, key := range months {
		m.PutMonth(*writeTestMonth(t, d, dir, mustMonth(t, key)))
	}
	m.PutDim(writeTestDims(t, dir))
	if err := WriteManifest(filepath.Join(dir, ManifestFile), m); err != nil {
		t.Fatal(err)
	}
	return dir, m
}

func writeTestDims(t *testing.T, dir string) FileEntry {
	t.Helper()
	name := filepath.Join(dir, filepath.FromSlash(DropPatternElementsFile))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	db := reader(t)
	if _, err := db.Exec(`COPY (SELECT (i % 20 + 1)::BIGINT AS drop_pattern_id, i::INTEGER AS item_id, 1::INTEGER AS quantity
		FROM range(60) r(i) ORDER BY 1, 2) TO ` + quote(name) + ` (` + parquetOptions + `)`); err != nil {
		t.Fatal(err)
	}
	fe, err := describeFile(name)
	if err != nil {
		t.Fatal(err)
	}
	fe.Path, fe.Rows = DropPatternElementsFile, 60
	return fe
}

// copyStore copies a cold storage directory to a new one.
func copyStore(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	copyDir(t, src, dir)
	return dir
}

func openTestReader(t *testing.T, dir string) *Reader {
	t.Helper()
	r, err := OpenReader(ReaderConfig{Dir: dir, Threads: 2, MemoryLimit: "256MB", TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func queryInt(t *testing.T, r *Reader, files *QueryFiles, query string) int64 {
	t.Helper()
	var n int64
	if err := r.QueryRow(context.Background(), files, query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestReaderServesTheManifestsFiles(t *testing.T) {
	d := newReaderTestData()
	dir, _ := writeReaderStore(t, d, readerMonths...)
	r := openTestReader(t, dir)
	v := r.View()

	wantBoundary := time.Date(2019, 7, 1, 0, 0, 0, 0, time.UTC)
	if !v.HotBoundary().Equal(wantBoundary) {
		t.Fatalf("hot boundary = %v, want %v", v.HotBoundary(), wantBoundary)
	}
	if got := testutil.ToFloat64(metricHotBoundary); got != float64(wantBoundary.Unix()) {
		t.Errorf("hot boundary metric = %v", got)
	}

	// a range that starts before the epoch and ends in the hot database
	start, end := time.Unix(0, 0).UTC(), time.Date(2019, 8, 15, 0, 0, 0, 0, time.UTC)
	cold, hot := v.Split(start, end)
	if !cold.Start.Equal(start) || !cold.End.Equal(wantBoundary) || !hot.Start.Equal(wantBoundary) || !hot.End.Equal(end) {
		t.Fatalf("split = %+v, %+v", cold, hot)
	}
	files := v.Files(KindDropReports, cold)
	if len(files) != 3 || files[0] != filepath.Join(dir, "drop_reports", "drop_reports_pre.parquet") {
		t.Fatalf("files = %v", files)
	}

	// what the files hold, against the test data
	var wantReports, wantRecruit, wantReliableTimes int64
	for _, rep := range d.reports {
		if !rep.createdAt.Before(cold.End) {
			continue
		}
		switch {
		case rep.stage == testRecruitStageID:
			wantRecruit++
		default:
			wantReports++
			if rep.reliability == 0 {
				wantReliableTimes += int64(rep.times)
			}
		}
	}
	window := " WHERE created_at >= TIMESTAMPTZ '1970-01-01 00:00:00+00' AND created_at < TIMESTAMPTZ '2019-07-01 00:00:00+00'"
	var f QueryFiles
	if n := queryInt(t, r, &f, "SELECT count(*) FROM "+f.ReadParquet(files)+window); n != wantReports || n == 0 {
		t.Errorf("reports = %d, want %d", n, wantReports)
	}
	if n := queryInt(t, r, &f, "SELECT count(*) FROM "+f.ReadParquet(v.Files(KindRecruit, cold))+window); n != wantRecruit || n == 0 {
		t.Errorf("recruit reports = %d, want %d", n, wantRecruit)
	}
	if n := queryInt(t, r, &f, "SELECT sum(times) FROM "+f.ReadParquet(v.Files(KindRollup, cold))); n != wantReliableTimes {
		t.Errorf("rollup times = %d, want %d", n, wantReliableTimes)
	}
	if n := queryInt(t, r, &f, "SELECT count(*) FROM "+f.ReadParquet([]string{v.DimFile(DropPatternElementsFile)})); n != 60 {
		t.Errorf("drop pattern elements = %d", n)
	}
	// the rollup's hours are UTC hours whatever the machine's zone
	if n := queryInt(t, r, &f, "SELECT count(*) FROM "+f.ReadParquet(v.Files(KindRollup, cold))+" WHERE hour <> date_trunc('hour', hour)"); n != 0 {
		t.Errorf("%d rollup hours are not whole hours", n)
	}

	// a range inside one month reads its file only
	june := TimeRange{Start: time.Date(2019, 6, 3, 0, 0, 0, 0, time.UTC), End: time.Date(2019, 6, 4, 0, 0, 0, 0, time.UTC)}
	if got := v.Files(KindExtras, june); len(got) != 1 || !strings.HasSuffix(got[0], "extras_2019-06.parquet") {
		t.Errorf("files of %+v = %v", june, got)
	}
	// nothing cold after the boundary
	if got := v.Files(KindDropReports, TimeRange{Start: wantBoundary, End: end}); got != nil {
		t.Errorf("files after the boundary = %v", got)
	}
}

func TestSplitAt(t *testing.T) {
	b := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		name                       string
		start, end                 time.Time
		coldEmpty, hotEmpty        bool
		coldStart, coldEnd, hotEnd time.Time
	}{
		{name: "across", start: b.Add(-day), end: b.Add(day), coldStart: b.Add(-day), coldEnd: b, hotEnd: b.Add(day)},
		{name: "all cold", start: b.Add(-2 * day), end: b.Add(-day), hotEmpty: true, coldStart: b.Add(-2 * day), coldEnd: b.Add(-day)},
		{name: "ends at the boundary", start: b.Add(-day), end: b, hotEmpty: true, coldStart: b.Add(-day), coldEnd: b},
		{name: "starts at the boundary", start: b, end: b.Add(day), coldEmpty: true, hotEnd: b.Add(day)},
		{name: "all hot", start: b.Add(day), end: b.Add(2 * day), coldEmpty: true, hotEnd: b.Add(2 * day)},
		{name: "empty", start: b, end: b, coldEmpty: true, hotEmpty: true},
	}
	for _, c := range cases {
		cold, hot := SplitAt(c.start, c.end, b)
		if cold.Empty() != c.coldEmpty || hot.Empty() != c.hotEmpty {
			t.Errorf("%s: cold %+v (empty %t), hot %+v (empty %t)", c.name, cold, cold.Empty(), hot, hot.Empty())
			continue
		}
		if !c.coldEmpty && (!cold.Start.Equal(c.coldStart) || !cold.End.Equal(c.coldEnd)) {
			t.Errorf("%s: cold = %+v", c.name, cold)
		}
		if !c.hotEmpty && (!hot.Start.Equal(laterOf(c.start, b)) || !hot.End.Equal(c.hotEnd)) {
			t.Errorf("%s: hot = %+v", c.name, hot)
		}
		// the parts are adjacent and cover the range
		if !c.coldEmpty && !c.hotEmpty && !cold.End.Equal(hot.Start) {
			t.Errorf("%s: cold ends at %v, hot starts at %v", c.name, cold.End, hot.Start)
		}
	}

	// nothing archived: everything is hot
	cold, hot := SplitAt(b, b.Add(day), HotBoundary(NewManifest()))
	if !cold.Empty() || !hot.Start.Equal(b) || !hot.End.Equal(b.Add(day)) {
		t.Errorf("without archive: cold %+v, hot %+v", cold, hot)
	}
}

func laterOf(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func TestCheckCoverage(t *testing.T) {
	entry := func(key string) MonthEntry {
		e := MonthEntry{Month: key}
		for _, kind := range Kinds {
			e.Files = append(e.Files, FileEntry{Path: FilePath(kind, key)})
		}
		return e
	}
	build := func(keys ...string) *Manifest {
		m := NewManifest()
		for _, k := range keys {
			m.PutMonth(entry(k))
		}
		m.PutDim(FileEntry{Path: DropPatternElementsFile})
		return m
	}
	if err := CheckCoverage(build("pre", "2019-05", "2019-06")); err != nil {
		t.Errorf("complete manifest: %v", err)
	}
	if err := CheckCoverage(NewManifest()); err != nil {
		t.Errorf("empty manifest: %v", err)
	}

	noFile := build("pre", "2019-05")
	noFile.Months[1].Files = noFile.Months[1].Files[:3]
	noDims := build("pre", "2019-05")
	noDims.Dims = nil
	for name, c := range map[string]struct {
		m    *Manifest
		want string
	}{
		"gap":           {build("pre", "2019-05", "2019-07"), "month 2019-06 is missing"},
		"no pre":        {build("2019-05", "2019-06"), "month pre is missing"},
		"late start":    {build("pre", "2019-06"), "month 2019-05 is missing"},
		"missing file":  {noFile, "month 2019-05 has no rollup file"},
		"missing dims":  {noDims, DropPatternElementsFile + " is missing"},
		"only regulars": {build("2026-07", "2026-08"), "month pre is missing"},
	} {
		err := CheckCoverage(c.m)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestOpenReaderRefusesAnIncompleteDirectory(t *testing.T) {
	d := newReaderTestData()
	src, _ := writeReaderStore(t, d, readerMonths...)

	cases := []struct {
		name     string
		breakDir func(dir string) error
		want     []string
	}{
		{"missing month file", func(dir string) error {
			return os.Remove(filepath.Join(dir, "recruit", "recruit_2019-06.parquet"))
		}, []string{"month 2019-06: recruit/recruit_2019-06.parquet is missing", "coldstore-pull --dir ", " --month 2019-06"}},
		{"truncated file", func(dir string) error {
			return os.Truncate(filepath.Join(dir, "drop_reports", "drop_reports_pre.parquet"), 100)
		}, []string{"month pre: drop_reports/drop_reports_pre.parquet has 100 bytes, the manifest says", "--month pre"}},
		{"symlinked file", func(dir string) error {
			// DuckDB would refuse to read it: allowed_directories holds the resolved directories, not each file's target
			f := filepath.Join(dir, "rollup", "rollup_2019-06.parquet")
			if err := os.Rename(f, filepath.Join(dir, "moved.parquet")); err != nil {
				return err
			}
			return os.Symlink(filepath.Join(dir, "moved.parquet"), f)
		}, []string{"month 2019-06: rollup/rollup_2019-06.parquet is a symbolic link", "--month 2019-06"}},
		{"missing dims", func(dir string) error {
			return os.Remove(filepath.Join(dir, filepath.FromSlash(DropPatternElementsFile)))
		}, []string{"dims: " + DropPatternElementsFile + " is missing", "coldstore-pull --dir ", " --dims"}},
		{"no manifest", func(dir string) error {
			return os.Remove(filepath.Join(dir, ManifestFile))
		}, []string{"no manifest in the cold storage directory"}},
		{"pending patch", func(dir string) error {
			if err := os.MkdirAll(filepath.Join(dir, JournalDir), 0o755); err != nil {
				return err
			}
			return os.WriteFile(journalPath(dir, MonthTarget("2019-05")), []byte("{}"), 0o644)
		}, []string{"month 2019-05: a patch stopped partway", "coldstore-recover"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := copyStore(t, src)
			if err := c.breakDir(dir); err != nil {
				t.Fatal(err)
			}
			tmp := t.TempDir()
			r, err := OpenReader(ReaderConfig{Dir: dir, Threads: 1, TempDir: tmp})
			if err == nil {
				_ = r.Close()
				t.Fatal("OpenReader succeeded")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v, want it to contain %q", err, w)
				}
			}
			// nothing is left behind
			if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
				t.Errorf("temp directory holds %d entries", len(entries))
			}
		})
	}
}

func TestReaderNeverReadsOutsideTheDirectory(t *testing.T) {
	d := newReaderTestData()
	dir, _ := writeReaderStore(t, d, readerMonths...)
	r := openTestReader(t, dir)

	outside := filepath.Join(t.TempDir(), "x.parquet")
	db := reader(t)
	if _, err := db.Exec("COPY (SELECT 1 AS x) TO " + quote(outside) + " (FORMAT parquet)"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, q := range []string{
		"SELECT count(*) FROM read_parquet(['s3://penguin-coldstore/coldstore/v1/drop_reports/drop_reports_2019-05.parquet'])",
		"SELECT count(*) FROM read_parquet(['http://127.0.0.1:1/drop_reports_2019-05.parquet'])",
		"SELECT count(*) FROM read_parquet(['https://example.com/drop_reports_2019-05.parquet'])",
		"SELECT count(*) FROM read_parquet([" + quote(outside) + "])",
		"SELECT count(*) FROM read_parquet([" + quote(filepath.Join(dir, "..", filepath.Base(filepath.Dir(outside)), "x.parquet")) + "])",
	} {
		var n int64
		err := r.QueryRow(ctx, nil, q).Scan(&n)
		if err == nil {
			t.Errorf("%s succeeded", q)
		} else if !strings.Contains(err.Error(), "disabled by configuration") {
			t.Errorf("%s: err = %v, want a permission error", q, err)
		}
	}
	for _, q := range []string{"SET enable_external_access = true", "LOAD httpfs", "INSTALL httpfs"} {
		if _, err := r.db.ExecContext(ctx, q); err == nil {
			t.Errorf("%s succeeded", q)
		}
	}
}

func TestReaderFollowsSymlinkedLayout(t *testing.T) {
	// a manifest next to symbolic links to another directory's subdirectories (see Manifest)
	d := newReaderTestData()
	src, _ := writeReaderStore(t, d, readerMonths...)
	dir := t.TempDir()
	for _, sub := range []string{"drop_reports", "drop_report_extras", "recruit", "rollup", "dims"} {
		if err := os.Symlink(filepath.Join(src, sub), filepath.Join(dir, sub)); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(filepath.Join(src, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), b, 0o644); err != nil {
		t.Fatal(err)
	}
	r := openTestReader(t, dir)
	v := r.View()
	cold, _ := v.Split(time.Unix(0, 0), v.HotBoundary())
	var f QueryFiles
	if n := queryInt(t, r, &f, "SELECT count(*) FROM "+f.ReadParquet(v.Files(KindDropReports, cold))); n == 0 {
		t.Error("no reports")
	}
}

func TestBuildManifestRefusesWhatOpenReaderRefuses(t *testing.T) {
	ctx := context.Background()
	d := newReaderTestData()
	src, m := writeReaderStore(t, d, readerMonths...)
	opts := Options{WorkDir: t.TempDir(), Threads: 2}

	// linked subdirectories are fine for both (see Manifest)
	linked := t.TempDir()
	for _, sub := range []string{"drop_reports", "drop_report_extras", "recruit", "rollup", "dims"} {
		if err := os.Symlink(filepath.Join(src, sub), filepath.Join(linked, sub)); err != nil {
			t.Fatal(err)
		}
	}
	built, err := BuildManifest(ctx, opts, linked)
	if err != nil {
		t.Fatalf("BuildManifest through linked subdirectories: %v", err)
	}
	if len(built.Months) != len(m.Months) || len(built.Dims) != 1 {
		t.Fatalf("built %d months, %d dims", len(built.Months), len(built.Dims))
	}
	for i := range m.Months {
		if b, w := built.Months[i], m.Months[i]; b.Month != w.Month || !reflect.DeepEqual(b.Files, w.Files) {
			t.Errorf("built %s = %+v, want %+v", b.Month, b.Files, w.Files)
		}
	}
	if err := CheckLocalFiles(linked, built); err != nil {
		t.Errorf("CheckLocalFiles through linked subdirectories: %v", err)
	}

	// a linked file is refused by both, with the same message
	for _, path := range []string{FilePath(KindRollup, "2019-06"), DropPatternElementsFile} {
		t.Run(path, func(t *testing.T) {
			dir := copyStore(t, src)
			f := filepath.Join(dir, filepath.FromSlash(path))
			if err := os.Rename(f, filepath.Join(dir, "moved.parquet")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(dir, "moved.parquet"), f); err != nil {
				t.Fatal(err)
			}
			want := path + " is a symbolic link; only the directory and its subdirectories may be links"
			if _, err := BuildManifest(ctx, opts, dir); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("BuildManifest err = %v, want %q", err, want)
			}
			if err := CheckLocalFiles(dir, m); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("CheckLocalFiles err = %v, want %q", err, want)
			}
		})
	}
}

func TestReaderReload(t *testing.T) {
	d := newReaderTestData()
	dir, m := writeReaderStore(t, d, "pre", "2019-05")
	r := openTestReader(t, dir)
	first := r.View()
	if want := time.Date(2019, 6, 1, 0, 0, 0, 0, time.UTC); !first.HotBoundary().Equal(want) {
		t.Fatalf("hot boundary = %v", first.HotBoundary())
	}
	if changed, err := r.Reload(); changed || err != nil {
		t.Fatalf("reload of the same manifest = %t, %v", changed, err)
	}

	// a month archived: its files first, then the manifest
	m.PutMonth(*writeTestMonth(t, d, dir, mustMonth(t, "2019-06")))
	if err := WriteManifest(filepath.Join(dir, ManifestFile), m); err != nil {
		t.Fatal(err)
	}
	if changed, err := r.Reload(); !changed || err != nil {
		t.Fatalf("reload = %t, %v", changed, err)
	}
	if want := time.Date(2019, 7, 1, 0, 0, 0, 0, time.UTC); !r.View().HotBoundary().Equal(want) {
		t.Errorf("hot boundary after the reload = %v", r.View().HotBoundary())
	}
	// the earlier view is unchanged for the queries that hold it
	if !first.HotBoundary().Equal(time.Date(2019, 6, 1, 0, 0, 0, 0, time.UTC)) || len(first.Manifest().Months) != 2 {
		t.Error("the earlier view changed")
	}

	// a manifest whose files are not there is not taken, and is tried again on every reload
	served := r.View()
	m.PutMonth(MonthEntry{Month: "2019-07", Files: []FileEntry{
		{Path: FilePath(KindDropReports, "2019-07"), Bytes: 1}, {Path: FilePath(KindExtras, "2019-07"), Bytes: 1},
		{Path: FilePath(KindRecruit, "2019-07"), Bytes: 1}, {Path: FilePath(KindRollup, "2019-07"), Bytes: 1},
	}})
	if err := WriteManifest(filepath.Join(dir, ManifestFile), m); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		changed, err := r.Reload()
		if changed || err == nil || !strings.Contains(err.Error(), "month 2019-07: drop_reports/drop_reports_2019-07.parquet is missing") {
			t.Fatalf("reload %d = %t, %v", i, changed, err)
		}
		if r.View() != served {
			t.Fatal("the view changed")
		}
	}

	// the background reload counts the failures
	before := testutil.ToFloat64(metricReloadFailures)
	r2, err := OpenReader(ReaderConfig{Dir: dir, Threads: 1, TempDir: t.TempDir(), ReloadInterval: 10 * time.Millisecond})
	if err == nil {
		_ = r2.Close()
		t.Fatal("OpenReader of the broken manifest succeeded")
	}
	m.Months = m.Months[:len(m.Months)-1]
	if err := WriteManifest(filepath.Join(dir, ManifestFile), m); err != nil {
		t.Fatal(err)
	}
	r2, err = OpenReader(ReaderConfig{Dir: dir, Threads: 1, TempDir: t.TempDir(), ReloadInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	r2.Start()
	if err := os.Remove(filepath.Join(dir, FilePath(KindRollup, "2019-06"))); err != nil {
		t.Fatal(err)
	}
	m.GeneratedAt = time.Now()
	if err := WriteManifest(filepath.Join(dir, ManifestFile), m); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for testutil.ToFloat64(metricReloadFailures) < before+2 {
		if time.Now().After(deadline) {
			t.Fatal("the background reload did not count its failures")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReaderReadsReplacedFiles(t *testing.T) {
	// a file replaced under the same name (a patch, a pull) is read again, not from the metadata cache
	d := newReaderTestData()
	dir, _ := writeReaderStore(t, d, readerMonths...)
	r := openTestReader(t, dir)
	v := r.View()
	var f QueryFiles
	files := f.ReadParquet(v.Files(KindDropReports, TimeRange{Start: Epoch, End: v.HotBoundary()}))
	before := queryInt(t, r, &f, "SELECT sum(times) FROM "+files)
	tamperFile(t, dir, KindDropReports, "2019-05", "times = times + 1000")
	if after := queryInt(t, r, &f, "SELECT sum(times) FROM "+files); after != before+1000 {
		t.Errorf("sum(times) = %d after the replacement, want %d", after, before+1000)
	}
}

func TestCheckIntegrity(t *testing.T) {
	d := newReaderTestData()
	dir, _ := writeReaderStore(t, d, readerMonths...)
	r := openTestReader(t, dir)
	ctx := context.Background()

	okBefore := testutil.ToFloat64(metricIntegrityFiles.WithLabelValues("ok"))
	res, err := r.CheckIntegrity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 3*len(Kinds)+1 || len(res.Bad) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if got := testutil.ToFloat64(metricIntegrityFiles.WithLabelValues("ok")) - okBefore; got != float64(res.Files) {
		t.Errorf("ok files counted = %v", got)
	}

	// a changed byte that keeps the size, and a missing file
	flipByte(t, dir, FilePath(KindExtras, "2019-06"))
	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(DropPatternElementsFile))); err != nil {
		t.Fatal(err)
	}
	mismatchBefore := testutil.ToFloat64(metricIntegrityFiles.WithLabelValues("mismatch"))
	missingBefore := testutil.ToFloat64(metricIntegrityFiles.WithLabelValues("missing"))
	res, err = r.CheckIntegrity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Join(res.Bad, "\n")
	if len(res.Bad) != 2 ||
		!strings.Contains(bad, DropPatternElementsFile+" is missing; restore it with: backend run-script coldstore-pull --dir "+dir+" --dims") ||
		!strings.Contains(bad, "drop_report_extras/extras_2019-06.parquet has ") ||
		!strings.Contains(bad, "--month 2019-06") {
		t.Errorf("bad files:\n%s", bad)
	}
	if testutil.ToFloat64(metricIntegrityFiles.WithLabelValues("mismatch"))-mismatchBefore != 1 ||
		testutil.ToFloat64(metricIntegrityFiles.WithLabelValues("missing"))-missingBefore != 1 {
		t.Error("the bad files were not counted")
	}
	if got := testutil.ToFloat64(metricIntegrityLastMismatches); got != 2 {
		t.Errorf("last bad files = %v", got)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.CheckIntegrity(cancelled); err == nil {
		t.Error("a cancelled check succeeded")
	}
}

func TestNextIntegrityCheck(t *testing.T) {
	state := filepath.Join(t.TempDir(), IntegrityStateFile)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	if got := nextIntegrityCheck(state, week, now); !got.Equal(now.Add(firstIntegrityCheckDelay)) {
		t.Errorf("without state: %v", got)
	}
	if got := nextIntegrityCheck(state, time.Minute, now); !got.Equal(now.Add(time.Minute)) {
		t.Errorf("without state, short interval: %v", got)
	}
	last := now.Add(-2 * 24 * time.Hour)
	if err := os.WriteFile(state, []byte(last.Format(time.RFC3339)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := nextIntegrityCheck(state, week, now); !got.Equal(last.Add(week)) {
		t.Errorf("with state: %v", got)
	}
	// overdue: right away
	if got := nextIntegrityCheck(state, 24*time.Hour, now); !got.Before(now) {
		t.Errorf("overdue: %v", got)
	}
	// a time in the future (clock change) is ignored
	if err := os.WriteFile(state, []byte(now.Add(time.Hour).Format(time.RFC3339)), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := nextIntegrityCheck(state, week, now); !got.Equal(now.Add(firstIntegrityCheckDelay)) {
		t.Errorf("future state: %v", got)
	}
}

func TestIntegrityLoopRecordsItsRuns(t *testing.T) {
	d := newReaderTestData()
	dir, _ := writeReaderStore(t, d, readerMonths...)
	tmp := t.TempDir()
	r, err := OpenReader(ReaderConfig{Dir: dir, Threads: 1, TempDir: tmp, IntegrityCheckInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	r.Start()
	state := filepath.Join(tmp, IntegrityStateFile)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(state); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no integrity check was recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	// the reader's own temp directory is gone, the state stays
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != IntegrityStateFile {
		t.Errorf("temp directory after Close: %v", entries)
	}
}

// TestIntegrityLoopDoesNotRecordAFailedCheck makes the integrity check fail on an unreadable manifest: the failure is
// not recorded as the last check, and the loop tries again and records the check once the manifest is readable.
func TestIntegrityLoopDoesNotRecordAFailedCheck(t *testing.T) {
	d := newReaderTestData()
	dir, _ := writeReaderStore(t, d, readerMonths...)
	tmp := t.TempDir()
	r, err := OpenReader(ReaderConfig{Dir: dir, Threads: 1, TempDir: tmp, IntegrityCheckInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	manifest := filepath.Join(dir, ManifestFile)
	good, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CheckIntegrity(context.Background()); err == nil {
		t.Fatal("CheckIntegrity accepted an unreadable manifest")
	}
	state := filepath.Join(tmp, IntegrityStateFile)
	r.Start()
	// with an interval of 20 ms, a few hundred milliseconds are many failed tries
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed check was recorded: %v", err)
	}
	if err := os.WriteFile(manifest, good, 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(state); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no integrity check was recorded after the manifest was repaired")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestIntegrityLoopWaitsWhenItCannotRecord keeps an overdue time in a state file that cannot be rewritten: the loop
// checks once and then waits the interval, instead of reading the old time back and checking again at once.
func TestIntegrityLoopWaitsWhenItCannotRecord(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes read-only files")
	}
	d := newReaderTestData()
	dir, _ := writeReaderStore(t, d, readerMonths...)
	tmp := t.TempDir()
	state := filepath.Join(tmp, IntegrityStateFile)
	if err := os.WriteFile(state, []byte(time.Now().Add(-48*time.Hour).UTC().Format(time.RFC3339)+"\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	probe := FilePath(KindRollup, "2019-05")
	var checks atomic.Int32
	testHookIntegrityChunk = func(path string) {
		if path == probe {
			checks.Add(1)
		}
	}
	t.Cleanup(func() { testHookIntegrityChunk = nil })
	r, err := OpenReader(ReaderConfig{Dir: dir, Threads: 1, TempDir: tmp, IntegrityCheckInterval: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	r.Start()
	deadline := time.Now().Add(10 * time.Second)
	for checks.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the overdue integrity check did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	if n := checks.Load(); n != 1 {
		t.Errorf("%d integrity checks ran, want 1", n)
	}
}

// flipByte changes a byte in the middle of a file of the cold storage directory, keeping its size.
func flipByte(t *testing.T, dir, path string) {
	t.Helper()
	name := filepath.Join(dir, filepath.FromSlash(path))
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)/2] ^= 0xff
	if err := os.WriteFile(name, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// growSparse makes a month's file of the cold storage directory a sparse file of 4 GiB, and its manifest entry agree
// with its size (not its SHA-256: the tests stop before its end), so that hashing it takes seconds.
func growSparse(t *testing.T, dir, path string) {
	t.Helper()
	const size = 4 << 30
	if err := os.Truncate(filepath.Join(dir, filepath.FromSlash(path)), size); err != nil {
		t.Fatal(err)
	}
	if err := UpdateManifest(filepath.Join(dir, ManifestFile), func(m *Manifest) error {
		for i := range m.Months {
			for k := range m.Months[i].Files {
				if m.Months[i].Files[k].Path == path {
					m.Months[i].Files[k].Bytes = size
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func isReliable(rep testReport) bool {
	return rep.stage != testRecruitStageID && rep.reliability == 0 && rep.times > 0
}

// TestCheckIntegrityFollowsPublishedPatches checks a month patched since the reader loaded the manifest: its new files
// match the manifest as it is now and are not reported, while the corruption of a file of an unchanged generation,
// the patched one included, still is.
func TestCheckIntegrityFollowsPublishedPatches(t *testing.T) {
	ctx := context.Background()
	d := newReaderTestData()
	dir, _ := writeReaderStore(t, d, readerMonths...)
	r := openTestReader(t, dir)
	june := mustMonth(t, "2019-06")
	opts := Options{WorkDir: t.TempDir(), Threads: 2}
	viewed := r.View()

	// a patch completed before the next reload
	report := pickReport(t, d, june, map[int64]bool{}, isReliable)
	if _, err := PatchMonth(ctx, opts, dir, june, Patch{Reliability: map[int64]int{report: -1}}, false); err != nil {
		t.Fatal(err)
	}
	if r.View() != viewed {
		t.Fatal("the view was reloaded")
	}
	res, err := r.CheckIntegrity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 3*len(Kinds)+1 || len(res.Bad) != 0 || len(res.Skipped) != 0 {
		t.Fatalf("result after a patch the view does not know = %+v", res)
	}
	if got := testutil.ToFloat64(metricIntegrityLastMismatches); got != 0 {
		t.Errorf("last bad files = %v", got)
	}

	// the patched generation and an unpatched month corrupted afterwards
	flipByte(t, dir, FilePath(KindDropReports, june.Key))
	flipByte(t, dir, FilePath(KindRollup, "2019-05"))
	res, err = r.CheckIntegrity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Join(res.Bad, "\n")
	if len(res.Bad) != 2 ||
		!strings.Contains(bad, FilePath(KindDropReports, june.Key)+" has ") || !strings.Contains(bad, "--month 2019-06") ||
		!strings.Contains(bad, FilePath(KindRollup, "2019-05")+" has ") || !strings.Contains(bad, "--month 2019-05") {
		t.Errorf("bad files:\n%s", bad)
	}
	if got := testutil.ToFloat64(metricIntegrityLastMismatches); got != 2 {
		t.Errorf("last bad files = %v", got)
	}
	flipByte(t, dir, FilePath(KindDropReports, june.Key))
	flipByte(t, dir, FilePath(KindRollup, "2019-05"))

	// a patch that stopped halfway through the swap of its files: its month is skipped, not reported, until it is
	// recovered
	injectAt(t, "swapped "+FilePath(KindDropReports, june.Key), errSimulatedCrash)
	report = pickReport(t, d, june, map[int64]bool{report: true}, isReliable)
	if _, err := PatchMonth(ctx, opts, dir, june, Patch{Reliability: map[int64]int{report: -1}}, false); !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("err = %v, want the simulated crash", err)
	}
	testHookPublishStep = nil
	res, err = r.CheckIntegrity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 2*len(Kinds)+1 || len(res.Bad) != 0 || !reflect.DeepEqual(res.Skipped, []string{june.Key}) {
		t.Errorf("result with a patch stopped partway = %+v", res)
	}
	if _, err := RecoverPatches(dir, []string{june.Key}); err != nil {
		t.Fatal(err)
	}
	if res, err = r.CheckIntegrity(ctx); err != nil || res.Files != 3*len(Kinds)+1 || len(res.Bad) != 0 || len(res.Skipped) != 0 {
		t.Errorf("result after the recovery = %+v, %v", res, err)
	}
}

// TestCheckIntegrityWaitsForPublications runs an integrity check and a patch of a month at the same time: a check
// that reaches the month halfway through the swap of its files waits for the end of the publication, and a patch
// that starts while the check hashes the month waits for the check. Neither reports a bad file.
func TestCheckIntegrityWaitsForPublications(t *testing.T) {
	ctx := context.Background()
	d := newReaderTestData()
	dir, _ := writeReaderStore(t, d, readerMonths...)
	r := openTestReader(t, dir)
	june := mustMonth(t, "2019-06")
	opts := Options{WorkDir: t.TempDir(), Threads: 2}
	used := map[int64]bool{}
	type outcome struct {
		res IntegrityResult
		err error
	}
	checkClean := func(what string, o outcome) {
		t.Helper()
		if o.err != nil || o.res.Files != 3*len(Kinds)+1 || len(o.res.Bad) != 0 || len(o.res.Skipped) != 0 {
			t.Fatalf("%s: %+v, %v", what, o.res, o.err)
		}
	}

	// the check reaches the month halfway through the swap of its files
	sharedWait := make(chan string, 1)
	testHookSharedLockWait = func(target string) {
		select {
		case sharedWait <- target:
		default:
		}
	}
	t.Cleanup(func() { testHookSharedLockWait = nil })
	midSwap := "swapped " + FilePath(KindDropReports, june.Key)
	checked := make(chan outcome, 1)
	testHookPublishStep = func(step string) error {
		if step != midSwap {
			return nil
		}
		go func() {
			res, err := r.CheckIntegrity(ctx)
			checked <- outcome{res, err}
		}()
		select {
		case target := <-sharedWait:
			if target != MonthTarget(june.Key) {
				t.Errorf("the check waits for %s", target)
			}
		case o := <-checked:
			t.Errorf("the check ran during the publication: %+v, %v", o.res, o.err)
			checked <- o
		case <-time.After(10 * time.Second):
			t.Error("the check neither waited nor ended")
		}
		return nil
	}
	t.Cleanup(func() { testHookPublishStep = nil })
	first := pickReport(t, d, june, used, isReliable)
	if _, err := PatchMonth(ctx, opts, dir, june, Patch{Reliability: map[int64]int{first: -1}}, false); err != nil {
		t.Fatal(err)
	}
	testHookPublishStep = nil
	checkClean("the check that waited for the publication", <-checked)

	// the patch starts while the check hashes the month
	lockWait := make(chan string, 1)
	testHookLockWait = func(target string) {
		select {
		case lockWait <- target:
		default:
		}
	}
	t.Cleanup(func() { testHookLockWait = nil })
	second := pickReport(t, d, june, used, isReliable)
	patched := make(chan error, 1)
	started := false
	testHookIntegrityChunk = func(path string) {
		if started || path != FilePath(KindDropReports, june.Key) {
			return
		}
		started = true
		go func() {
			_, err := PatchMonth(ctx, opts, dir, june, Patch{Reliability: map[int64]int{second: -1}}, false)
			patched <- err
		}()
		select {
		case target := <-lockWait:
			if target != MonthTarget(june.Key) {
				t.Errorf("the patch waits for %s", target)
			}
		case err := <-patched:
			t.Errorf("the patch ran while the check hashed the month: %v", err)
			patched <- err
		case <-time.After(10 * time.Second):
			t.Error("the patch neither waited nor ended")
		}
	}
	t.Cleanup(func() { testHookIntegrityChunk = nil })
	res, err := r.CheckIntegrity(ctx)
	testHookIntegrityChunk = nil
	checkClean("the check the patch waited for", outcome{res, err})
	if !started {
		t.Fatal("the check never hashed the month")
	}
	if err := <-patched; err != nil {
		t.Fatal(err)
	}
	res, err = r.CheckIntegrity(ctx)
	checkClean("the check after the patch", outcome{res, err})
}

// TestCheckIntegrityStopsWithinAFile cancels an integrity check once it has begun to hash a large file: it stops
// within the file, returns the context's error and is not counted as a completed check.
func TestCheckIntegrityStopsWithinAFile(t *testing.T) {
	d := newReaderTestData()
	dir, _ := writeReaderStore(t, d, readerMonths...)
	big := FilePath(KindExtras, "2019-06")
	growSparse(t, dir, big)
	r := openTestReader(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chunks := 0
	testHookIntegrityChunk = func(path string) {
		if path == big {
			chunks++
			cancel()
		}
	}
	t.Cleanup(func() { testHookIntegrityChunk = nil })
	metricIntegrityLastCompleted.Set(1)
	metricIntegrityLastMismatches.Set(7)
	if _, err := r.CheckIntegrity(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
	if chunks != 1 {
		t.Errorf("%d chunks of %s hashed, want the one before the cancellation", chunks, big)
	}
	if testutil.ToFloat64(metricIntegrityLastCompleted) != 1 || testutil.ToFloat64(metricIntegrityLastMismatches) != 7 {
		t.Error("the cancelled check was counted as completed")
	}
}

// TestCloseInterruptsTheIntegrityCheck closes a Reader while its background integrity check hashes a large file:
// Close does not wait for the end of the file, and the interrupted check is not recorded as the last one.
func TestCloseInterruptsTheIntegrityCheck(t *testing.T) {
	d := newReaderTestData()
	dir, _ := writeReaderStore(t, d, readerMonths...)
	big := FilePath(KindExtras, "2019-06")
	growSparse(t, dir, big)
	tmp := t.TempDir()
	hashing := make(chan struct{})
	var once sync.Once
	testHookIntegrityChunk = func(path string) {
		if path == big {
			once.Do(func() { close(hashing) })
		}
	}
	t.Cleanup(func() { testHookIntegrityChunk = nil })
	r, err := OpenReader(ReaderConfig{Dir: dir, Threads: 1, TempDir: tmp, IntegrityCheckInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	r.Start()
	select {
	case <-hashing:
	case <-time.After(10 * time.Second):
		t.Fatal("the integrity check did not reach the large file")
	}
	started := time.Now()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 2*time.Second {
		t.Errorf("Close took %v", took)
	}
	if _, err := os.Stat(filepath.Join(tmp, IntegrityStateFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the interrupted check was recorded: %v", err)
	}
}

// TestReaderNeverServesTwoGenerationsOfAMonth patches a month while a Reader serves it: a query that runs during the
// publication holds it back until its rows are closed, a query that starts during it waits for its end, and after a
// publication that crashed the queries of the month fail until the patch is recovered. Each query compares the
// month's reliable reports with its rollup, which agree in every generation and not across two.
func TestReaderNeverServesTwoGenerationsOfAMonth(t *testing.T) {
	ctx := context.Background()
	d := newReaderTestData()
	dir, _ := writeReaderStore(t, d, readerMonths...)
	r := openTestReader(t, dir)
	june, may := mustMonth(t, "2019-06"), mustMonth(t, "2019-05")
	opts := Options{WorkDir: t.TempDir(), Threads: 2}

	// the reliable times of the month in drop_reports, and its rollup's
	monthTimes := func(ctx context.Context, month Month) (raw, rollup int64, err error) {
		v := r.View()
		rng := TimeRange{Start: month.Start, End: month.End}
		var f QueryFiles
		err = r.QueryRow(ctx, &f, `SELECT
				(SELECT coalesce(sum(times), 0) FROM `+f.ReadParquet(v.Files(KindDropReports, rng))+` WHERE reliability = 0),
				(SELECT coalesce(sum(times), 0) FROM `+f.ReadParquet(v.Files(KindRollup, rng))+`)`).Scan(&raw, &rollup)
		return raw, rollup, err
	}
	checkWhole := func(what string, month Month) int64 {
		t.Helper()
		raw, rollup, err := monthTimes(ctx, month)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if raw != rollup || raw == 0 {
			t.Fatalf("%s: reliable times %d, rollup %d: two generations", what, raw, rollup)
		}
		return raw
	}
	used := map[int64]bool{}
	reliable := func(rep testReport) bool {
		return rep.stage != testRecruitStageID && rep.reliability == 0 && rep.times > 0
	}
	timesOf := func(id int64) int64 {
		for _, rep := range d.reports {
			if rep.id == id {
				return int64(rep.times)
			}
		}
		t.Fatalf("no report %d", id)
		return 0
	}
	before := checkWhole("before", june)

	// a query running when the patch starts: the publication waits until its rows are closed
	v := r.View()
	var f QueryFiles
	rows, err := r.Query(ctx, &f, "SELECT count(*) FROM "+f.ReadParquet(v.Files(KindRollup, TimeRange{Start: june.Start, End: june.End})))
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan string, 1)
	testHookLockWait = func(target string) { waiting <- target }
	t.Cleanup(func() { testHookLockWait = nil })
	first := pickReport(t, d, june, used, reliable)
	patched := make(chan error, 1)
	go func() {
		_, err := PatchMonth(ctx, opts, dir, june, Patch{Reliability: map[int64]int{first: -1}}, false)
		patched <- err
	}()
	select {
	case target := <-waiting:
		if target != MonthTarget(june.Key) {
			t.Fatalf("the patch waits for %s", target)
		}
	case err := <-patched:
		t.Fatalf("the patch ran while a query read the month: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the patch neither waited nor ended")
	}
	select {
	case err := <-patched:
		t.Fatalf("the patch ran while a query read the month: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-patched; err != nil {
		t.Fatal(err)
	}
	testHookLockWait = nil
	if got := checkWhole("after the first patch", june); got != before-timesOf(first) {
		t.Fatalf("reliable times after the first patch = %d, want %d", got, before-timesOf(first))
	}
	before -= timesOf(first)

	// queries that start halfway through the swap of the files: they wait for the end of the publication, or fail
	// when their context ends first
	second := pickReport(t, d, june, used, reliable)
	midSwap := "swapped " + FilePath(KindDropReports, june.Key)
	waited := make(chan error, 1)
	var waitedRaw, waitedRollup int64
	testHookPublishStep = func(step string) error {
		if step != midSwap {
			return nil
		}
		shortCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		if raw, rollup, err := monthTimes(shortCtx, june); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("a query during the swap = %d, %d, %v; want it to wait until its context ends", raw, rollup, err)
		}
		// a query of another month does not wait
		otherCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if _, _, err := monthTimes(otherCtx, may); err != nil {
			t.Errorf("a query of another month during the swap: %v", err)
		}
		go func() {
			var err error
			waitedRaw, waitedRollup, err = monthTimes(ctx, june)
			waited <- err
		}()
		return nil
	}
	t.Cleanup(func() { testHookPublishStep = nil })
	if _, err := PatchMonth(ctx, opts, dir, june, Patch{Reliability: map[int64]int{second: -1}}, false); err != nil {
		t.Fatal(err)
	}
	testHookPublishStep = nil
	if err := <-waited; err != nil {
		t.Fatal(err)
	}
	if want := before - timesOf(second); waitedRaw != want || waitedRollup != want {
		t.Fatalf("the query that waited for the publication read %d, rollup %d, want %d", waitedRaw, waitedRollup, want)
	}
	before -= timesOf(second)

	// a publication that crashed halfway through the swap: the month's queries fail until it is recovered, and
	// those of the other months are served
	third := pickReport(t, d, june, used, reliable)
	injectAt(t, midSwap, errSimulatedCrash)
	if _, err := PatchMonth(ctx, opts, dir, june, Patch{Reliability: map[int64]int{third: -1}}, false); !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("err = %v, want the simulated crash", err)
	}
	testHookPublishStep = nil
	if changed, err := r.Reload(); changed || err == nil {
		t.Errorf("reload after the crash = %t, %v; want a refusal", changed, err)
	}
	for i := 0; i < 2; i++ {
		raw, rollup, err := monthTimes(ctx, june)
		if err == nil || !strings.Contains(err.Error(), "a patch of 2019-06 stopped partway") ||
			!strings.Contains(err.Error(), "coldstore-recover --dir "+dir+" --month 2019-06") {
			t.Fatalf("query %d of the month after the crash = %d, %d, %v; want a refusal", i, raw, rollup, err)
		}
	}
	checkWhole("another month after the crash", may)
	if _, err := RecoverPatches(dir, []string{june.Key}); err != nil {
		t.Fatal(err)
	}
	if got := checkWhole("after the recovery", june); got != before-timesOf(third) {
		t.Fatalf("reliable times after the recovery = %d, want %d", got, before-timesOf(third))
	}
}

func TestReaderLocksOnlyFilesOfTheColdStorage(t *testing.T) {
	d := newReaderTestData()
	dir, _ := writeReaderStore(t, d, readerMonths...)
	r := openTestReader(t, dir)
	for _, p := range []string{
		filepath.Join(dir, "manifest.json"),
		filepath.Join(dir, "drop_reports", "other.parquet"),
		filepath.Join(t.TempDir(), FilePath(KindDropReports, "2019-05")),
	} {
		f := &QueryFiles{paths: []string{p}}
		if _, err := r.Query(context.Background(), f, "SELECT 1"); err == nil || !strings.Contains(err.Error(), "is not a file of the cold storage") {
			t.Errorf("query over %s: err = %v", p, err)
		}
	}
	for _, c := range []struct{ rel, target string }{
		{DropPatternElementsFile, DimsTarget},
		{FilePath(KindRollup, "pre"), MonthTarget("pre")},
		{FilePath(KindExtras, "2019-06"), MonthTarget("2019-06")},
	} {
		if got, ok := targetOfFile(c.rel); !ok || got != c.target {
			t.Errorf("targetOfFile(%s) = %s, %t; want %s", c.rel, got, ok, c.target)
		}
	}
	targets := []string{MonthTarget("2019-05"), DimsTarget, MonthTarget("pre"), MonthTarget("2019-10")}
	sort.Slice(targets, func(i, k int) bool { return targetLess(targets[i], targets[k]) })
	if want := []string{DimsTarget, MonthTarget("pre"), MonthTarget("2019-05"), MonthTarget("2019-10")}; !reflect.DeepEqual(targets, want) {
		t.Errorf("lock order = %v, want %v", targets, want)
	}
}
