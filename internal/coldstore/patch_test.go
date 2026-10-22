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

// applyTestPatch returns a copy of the test data with the patch applied, as an admin fix in PostgreSQL would.
func applyTestPatch(d *testData, p Patch) *testData {
	del := map[int64]bool{}
	for _, id := range p.Delete {
		del[id] = true
	}
	out := &testData{extras: d.extras}
	for _, r := range d.reports {
		if del[r.id] {
			continue
		}
		if rel, ok := p.Reliability[r.id]; ok {
			r.reliability = rel
		}
		out.reports = append(out.reports, r)
	}
	return out
}

// pickReport returns the id of the first report of the month that matches.
func pickReport(t *testing.T, d *testData, m Month, skip map[int64]bool, match func(testReport) bool) int64 {
	t.Helper()
	for _, r := range d.reports {
		if d.inMonth(m)(r) && !skip[r.id] && match(r) {
			skip[r.id] = true
			return r.id
		}
	}
	t.Fatalf("no report in %s matches", m.Key)
	return 0
}

func TestPatchMonth(t *testing.T) {
	ctx := context.Background()
	d := newTestData()
	dir := t.TempDir()
	opts := Options{WorkDir: t.TempDir(), Threads: 2}
	manifest := filepath.Join(dir, ManifestFile)
	for _, key := range testMonths {
		e := writeTestMonth(t, d, dir, mustMonth(t, key))
		if err := UpdateManifest(manifest, func(mf *Manifest) error { mf.PutMonth(*e); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	mf, err := ReadManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}

	aug, sep := mustMonth(t, "2026-08"), mustMonth(t, "2026-09")
	used := map[int64]bool{}
	hasExtras := func(r testReport) bool { return r.id%13 != 0 }
	p := Patch{
		Delete: []int64{
			pickReport(t, d, sep, used, func(r testReport) bool { return r.stage != testRecruitStageID && r.reliability == 0 && hasExtras(r) }),
			pickReport(t, d, sep, used, func(r testReport) bool { return r.stage == testRecruitStageID && hasExtras(r) }),
		},
		Reliability: map[int64]int{
			// the app's delete: leaves the rollup
			pickReport(t, d, sep, used, func(r testReport) bool { return r.stage != testRecruitStageID && r.reliability == 0 }): -1,
			// joins the rollup
			pickReport(t, d, sep, used, func(r testReport) bool { return r.stage != testRecruitStageID && r.reliability == 2 }): 0,
			pickReport(t, d, sep, used, func(r testReport) bool { return r.stage == testRecruitStageID && r.reliability == 0 }): 1,
			pickReport(t, d, aug, used, func(r testReport) bool { return r.stage != testRecruitStageID && r.reliability == 1 }): 0,
		},
	}
	unchanged := pickReport(t, d, sep, used, func(r testReport) bool { return r.reliability == 1 })
	p.Reliability[unchanged] = 1
	const missing = 1500 // the orphan extras row: inside the months' report_id range, but no report
	p.Delete = append(p.Delete, missing)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}

	found, err := LocateReports(ctx, opts, dir, mf, nil, p.IDs())
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || len(found["2026-09"]) != 6 || len(found["2026-08"]) != 1 {
		t.Fatalf("found = %v", found)
	}
	if only, err := LocateReports(ctx, opts, dir, mf, []string{"2026-08"}, p.IDs()); err != nil || len(only) != 1 || len(only["2026-08"]) != 1 {
		t.Errorf("found in 2026-08 = %v, %v", only, err)
	}

	before := listFiles(t, dir)
	dry, err := PatchMonth(ctx, opts, dir, sep, p, true)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Entry != nil || dry.Deleted != 2 || dry.Updated != 3 || len(dry.Found) != 6 {
		t.Errorf("dry run = %+v", dry)
	}
	if err := VerifyMonth(ctx, opts, dir, mf, sep, false); err != nil || !reflect.DeepEqual(before, listFiles(t, dir)) {
		t.Errorf("the dry run changed the files: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, LocksDir)); err == nil {
		t.Error("the dry run created the lock directory")
	}
	// a dry run on a read-only copy, with and without lock files
	for _, withLocks := range []bool{false, true} {
		ro := t.TempDir()
		copyDir(t, dir, ro)
		if withLocks {
			if err := os.MkdirAll(filepath.Join(ro, LocksDir), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(ro, LocksDir, MonthTarget(sep.Key)+".lock"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		setTreeMode(t, ro, 0o555)
		t.Cleanup(func() { setTreeMode(t, ro, 0o755) })
		if got, err := PatchMonth(ctx, opts, ro, sep, p, true); err != nil || got.Deleted != 2 || got.Updated != 3 {
			t.Errorf("dry run on a read-only copy (lock files: %v) = %+v, %v", withLocks, got, err)
		}
	}

	patched := applyTestPatch(d, p)
	for _, m := range []Month{sep, aug} {
		res, err := PatchMonth(ctx, opts, dir, m, p.Only(found[m.Key]), false)
		if err != nil {
			t.Fatalf("patch %s: %v", m.Key, err)
		}
		if res.Entry == nil {
			t.Fatalf("patch %s wrote nothing: %+v", m.Key, res)
		}
		checkMonthFiles(t, patched, dir, res.Entry, m)
		// the patched files are the files an export of the fixed rows writes, byte for byte
		want := writeTestMonth(t, patched, t.TempDir(), m)
		if !reflect.DeepEqual(res.Entry.Files, want.Files) || !reflect.DeepEqual(res.Entry.MonthStats, want.MonthStats) {
			t.Errorf("patched %s:\n%+v\nwant\n%+v", m.Key, res.Entry, want)
		}
		if old := mf.Month(m.Key); old.ReportsHash == res.Entry.ReportsHash {
			t.Errorf("%s reports hash unchanged", m.Key)
		}
		// recorded in the manifest by PatchMonth
		if got, err := ReadManifest(manifest); err != nil || !reflect.DeepEqual(got.Month(m.Key).Files, res.Entry.Files) {
			t.Errorf("manifest entry of %s after the patch: %v", m.Key, err)
		}
		checkNoPublicationLeftovers(t, dir)
	}
	if sepEntry := writeTestMonth(t, patched, t.TempDir(), sep); sepEntry.ExtrasRows != mf.Month("2026-09").ExtrasRows-2 {
		t.Errorf("extras rows %d, before %d: the deleted reports' extras are kept", sepEntry.ExtrasRows, mf.Month("2026-09").ExtrasRows)
	}

	mf, err = ReadManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range testMonths {
		if err := VerifyMonth(ctx, opts, dir, mf, mustMonth(t, key), false); err != nil {
			t.Errorf("verify %s after the patch: %v", key, err)
		}
	}

	// applying it again changes nothing: the deleted reports are gone, the reliabilities set
	again, err := PatchMonth(ctx, opts, dir, sep, p.Only(found["2026-09"]), false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Entry != nil || again.Deleted != 0 || again.Updated != 0 || len(again.Found) != 4 {
		t.Errorf("second patch = %+v", again)
	}

	// files that do not match the manifest are not patched
	tamperFile(t, dir, KindDropReports, sep.Key, "account_id = account_id + 1")
	_, err = PatchMonth(ctx, opts, dir, sep, Patch{Reliability: map[int64]int{unchanged: 5}}, false)
	if !IsVerificationError(err) || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("patch of tampered files = %v", err)
	}
	// nor files whose manifest entry does not match their content
	rebuilt := NewManifest()
	e := *mf.Month(sep.Key)
	rebuiltEntry, err := BuildManifest(ctx, opts, dir)
	if err != nil {
		t.Fatal(err)
	}
	e.Files = rebuiltEntry.Month(sep.Key).Files
	rebuilt.PutMonth(e)
	if err := WriteManifest(manifest, rebuilt); err != nil {
		t.Fatal(err)
	}
	_, err = PatchMonth(ctx, opts, dir, sep, Patch{Reliability: map[int64]int{unchanged: 5}}, false)
	if !IsVerificationError(err) || !strings.Contains(err.Error(), "files vs manifest: reports_hash") {
		t.Errorf("patch of files that differ from their entry = %v", err)
	}
}

func TestPatchValidate(t *testing.T) {
	cases := []struct {
		p    Patch
		want string
	}{
		{Patch{}, "empty"},
		{Patch{Delete: []int64{1, 1}}, "twice"},
		{Patch{Delete: []int64{1}, Reliability: map[int64]int{1: -1}}, "both"},
		{Patch{Delete: []int64{2}, Reliability: map[int64]int{1: -1}}, ""},
	}
	for _, c := range cases {
		err := c.p.Validate()
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("Validate(%+v) = %v, want %q", c.p, err, c.want)
		}
	}
	p := Patch{Delete: []int64{5, 1}, Reliability: map[int64]int{3: 0, 4: -1}}
	if ids := p.IDs(); !reflect.DeepEqual(ids, []int64{1, 3, 4, 5}) {
		t.Errorf("IDs = %v", ids)
	}
	if o := p.Only([]int64{1, 4}); !reflect.DeepEqual(o, Patch{Delete: []int64{1}, Reliability: map[int64]int{4: -1}}) {
		t.Errorf("Only = %+v", o)
	}
}

// setTreeMode sets the mode of dir and of the directories under it.
func setTreeMode(t *testing.T, dir string, mode fs.FileMode) {
	t.Helper()
	var dirs []string
	err := filepath.WalkDir(dir, func(p string, de fs.DirEntry, err error) error {
		if err == nil && de.IsDir() {
			dirs = append(dirs, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// the deepest first, so that a read-only parent does not hide its children
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i], mode); err != nil {
			t.Fatal(err)
		}
	}
}
