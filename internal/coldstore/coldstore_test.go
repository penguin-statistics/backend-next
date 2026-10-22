package coldstore

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeTestMonth writes a month of the test data with the exporter's SQL, from tables filled in DuckDB the way
// loadSource fills them from PostgreSQL (see export_integration_test.go for the export from PostgreSQL).
func writeTestMonth(t *testing.T, d *testData, dir string, m Month) *MonthEntry {
	t.Helper()
	ctx := context.Background()
	j, err := newJob(ctx, Options{WorkDir: t.TempDir(), Threads: 2, MemoryLimit: "512MB"}, "test-"+m.Key, false)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	stmts := []string{
		`CREATE TABLE r (report_id BIGINT, stage_id INTEGER, pattern_id BIGINT, times INTEGER, created_at TIMESTAMPTZ,
			reliability INTEGER, server VARCHAR, account_id BIGINT, source_name VARCHAR, version VARCHAR)`,
		`CREATE TABLE e (report_id BIGINT, ip VARCHAR, metadata VARCHAR, md5 VARCHAR)`,
	}
	for _, s := range append(stmts, d.insertSQL("r", "e", d.inMonth(m))...) {
		if err := j.exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	e, err := j.writeMonth(ctx, m, dir, testRecruitStageID, nil)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func mustMonth(t *testing.T, key string) Month {
	t.Helper()
	m, err := ParseMonth(key)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func listFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, de fs.DirEntry, err error) error {
		// the publications' lock files are not part of the cold storage
		if err == nil && de.IsDir() && de.Name() == LocksDir {
			return filepath.SkipDir
		}
		if err == nil && !de.IsDir() {
			out = append(out, strings.TrimPrefix(p, dir))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func verificationProblems(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("verification passed, want a failure")
	}
	v, ok := err.(*VerificationError)
	if !ok {
		t.Fatalf("verification error = %v, want a *VerificationError", err)
	}
	return strings.Join(v.Problems, "\n")
}

var testMonths = []string{"pre", "2026-07", "2026-08", "2026-09"}

func TestWriteMonthBuildManifestAndVerify(t *testing.T) {
	ctx := context.Background()
	d := newTestData()
	dir := t.TempDir()
	opts := Options{WorkDir: t.TempDir(), Threads: 2}
	exported := map[string]*MonthEntry{}
	for _, key := range testMonths {
		m := mustMonth(t, key)
		e := writeTestMonth(t, d, dir, m)
		checkMonthFiles(t, d, dir, e, m)
		exported[key] = e
		if err := UpdateManifest(filepath.Join(dir, ManifestFile), func(mf *Manifest) error {
			mf.PutMonth(*e)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if exported["2026-07"].Rows != 0 || exported["pre"].Rows != 2 || exported["2026-08"].RecruitRows == 0 {
		t.Errorf("rows: 2026-07 %d, pre %d, 2026-08 recruit %d", exported["2026-07"].Rows, exported["pre"].Rows, exported["2026-08"].RecruitRows)
	}
	for _, f := range listFiles(t, dir) {
		if strings.HasSuffix(f, ".tmp") {
			t.Errorf("temporary file left: %s", f)
		}
	}

	mf, err := ReadManifest(filepath.Join(dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range testMonths {
		if err := VerifyMonth(ctx, opts, dir, mf, mustMonth(t, key), false); err != nil {
			t.Errorf("verify %s: %v", key, err)
		}
	}

	// the same rows give the same files
	again := writeTestMonth(t, d, t.TempDir(), mustMonth(t, "2026-08"))
	if !reflect.DeepEqual(again.Files, exported["2026-08"].Files) {
		t.Errorf("a second export wrote other files:\n%+v\nwant\n%+v", again.Files, exported["2026-08"].Files)
	}

	// indexing the directory reproduces the export's entries, without writing to it
	before := listFiles(t, dir)
	built, err := BuildManifest(ctx, opts, dir)
	if err != nil {
		t.Fatal(err)
	}
	if after := listFiles(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("BuildManifest changed the directory: %v -> %v", before, after)
	}
	if len(built.Months) != len(testMonths) || built.Months[0].Month != "pre" {
		t.Fatalf("built months = %+v", built.Months)
	}
	for _, key := range testMonths {
		b, e := built.Month(key), exported[key]
		if !reflect.DeepEqual(b.MonthStats, e.MonthStats) || !reflect.DeepEqual(b.Files, e.Files) || b.RecruitStageID != e.RecruitStageID {
			t.Errorf("built %s = %+v\nexported %+v", key, b, e)
		}
	}

	// a manifest written elsewhere indexes the files through a directory of links to them
	linked := t.TempDir()
	for _, kind := range Kinds {
		if err := os.Symlink(filepath.Join(dir, string(kind)), filepath.Join(linked, string(kind))); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteManifest(filepath.Join(linked, ManifestFile), built); err != nil {
		t.Fatal(err)
	}
	lmf, err := ReadManifest(filepath.Join(linked, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyMonth(ctx, opts, linked, lmf, mustMonth(t, "2026-09"), false); err != nil {
		t.Errorf("verify through links: %v", err)
	}
}

func TestVerifyCatchesTamperedFiles(t *testing.T) {
	ctx := context.Background()
	d := newTestData()
	opts := Options{WorkDir: t.TempDir(), Threads: 2}
	m := mustMonth(t, "2026-09")

	cases := []struct {
		name, set string
		kind      Kind
		want      []string
	}{
		{"times", "times = times + 1", KindDropReports, []string{"sha256", "reports_hash", "sum_times"}},
		// changes neither a count nor a sum nor a bound: only the content hash sees it
		{"account", "account_id = account_id + 1", KindDropReports, []string{"sha256", "reports_hash"}},
		{"source", "source_name = 'tampered'", KindDropReports, []string{"sha256", "reports_hash"}},
		{"recruit", "pattern_id = pattern_id + 1", KindRecruit, []string{"sha256", "reports_hash"}},
		{"extras", "ip = '10.255.255.0/24'", KindExtras, []string{"sha256", "extras_hash"}},
		{"rollup", "cnt = cnt + 1", KindRollup, []string{"sha256", "rollup: cnt"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			e := writeTestMonth(t, d, dir, m)
			mf := NewManifest()
			mf.PutMonth(*e)
			tamperFile(t, dir, c.kind, m.Key, c.set)

			problems := verificationProblems(t, VerifyMonth(ctx, opts, dir, mf, m, false))
			for _, w := range c.want {
				if !strings.Contains(problems, w) {
					t.Errorf("problems do not mention %q:\n%s", w, problems)
				}
			}
			if c.name == "account" && strings.Contains(problems, "rows") {
				t.Errorf("unexpected problems:\n%s", problems)
			}

			// a manifest built from the tampered files agrees with them: only the source can tell (see the
			// integration test); a tampered rollup is caught anyway
			rebuilt, err := BuildManifest(ctx, opts, dir)
			if c.kind == KindRollup {
				if err == nil || !strings.Contains(err.Error(), "rollup") {
					t.Errorf("BuildManifest of a tampered rollup = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyMonth(ctx, opts, dir, rebuilt, m, false); err != nil {
				t.Errorf("verify against the rebuilt manifest: %v", err)
			}
		})
	}

	t.Run("missing", func(t *testing.T) {
		dir := t.TempDir()
		e := writeTestMonth(t, d, dir, m)
		mf := NewManifest()
		mf.PutMonth(*e)
		if err := os.Remove(filepath.Join(dir, FilePath(KindRecruit, m.Key))); err != nil {
			t.Fatal(err)
		}
		if err := VerifyMonth(ctx, opts, dir, mf, m, false); err == nil || IsVerificationError(err) {
			t.Errorf("verify without the recruit file = %v, want an error", err)
		}
		if err := VerifyMonth(ctx, opts, dir, mf, mustMonth(t, "2026-08"), false); err == nil {
			t.Error("verify of a month not in the manifest passed")
		}
	})
}
