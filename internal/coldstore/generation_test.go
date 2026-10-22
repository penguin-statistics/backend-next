package coldstore

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pkg/errors"
)

// checkNoPublicationLeftovers checks that no journal, .next, .prev or temporary file is left in dir.
func checkNoPublicationLeftovers(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if strings.HasPrefix(rel, JournalDir+string(filepath.Separator)) || strings.HasSuffix(p, ".next") ||
			strings.HasSuffix(p, ".prev") || strings.HasSuffix(p, ".tmp") {
			t.Errorf("left: %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if de.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// checkMonthState checks that the month's manifest entry lists want and that the files pass VerifyMonth against
// it, without PostgreSQL.
func checkMonthState(t *testing.T, opts Options, dir string, month Month, want []FileEntry) {
	t.Helper()
	m, err := ReadManifest(filepath.Join(dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Month(month.Key).Files; !reflect.DeepEqual(got, want) {
		t.Errorf("manifest lists\n%+v\nwant\n%+v", got, want)
	}
	if err := VerifyMonth(context.Background(), opts, dir, m, month, false); err != nil {
		t.Errorf("verify: %v", err)
	}
	checkNoPublicationLeftovers(t, dir)
}

// injectAt makes publishGeneration fail once at a step, with err.
func injectAt(t *testing.T, step string, err error) {
	t.Helper()
	fired := false
	testHookPublishStep = func(s string) error {
		if s == step && !fired {
			fired = true
			return err
		}
		return nil
	}
	t.Cleanup(func() { testHookPublishStep = nil })
}

// TestPatchPublicationFailures stops a patch at every step of its publication, by an error and by a simulated
// crash (nothing done after the step, not even the recovery an error triggers), and checks that the month ends up
// whole, either the previous generation or the new one, with a manifest entry that describes it, without
// PostgreSQL: right away after an error, and after coldstore-recover (RecoverPatches) or the next patch after a
// crash.
func TestPatchPublicationFailures(t *testing.T) {
	ctx := context.Background()
	d := newTestData()
	sep := mustMonth(t, "2026-09")
	opts := Options{WorkDir: t.TempDir(), Threads: 2}

	base := t.TempDir()
	old := writeTestMonth(t, d, base, sep)
	if err := UpdateManifest(filepath.Join(base, ManifestFile), func(m *Manifest) error { m.PutMonth(*old); return nil }); err != nil {
		t.Fatal(err)
	}
	used := map[int64]bool{}
	p := Patch{Reliability: map[int64]int{
		pickReport(t, d, sep, used, func(r testReport) bool { return r.stage != testRecruitStageID && r.reliability == 0 }): -1,
	}}
	want := writeTestMonth(t, applyTestPatch(d, p), t.TempDir(), sep).Files

	steps := []string{"staged", "journaled", "committed"}
	for _, kind := range Kinds {
		steps = append(steps, "swapped "+FilePath(kind, sep.Key))
	}
	steps = append(steps, "cleaned")
	committed := func(step string) bool { return step != "staged" && step != "journaled" }

	for _, step := range steps {
		t.Run("error at "+step, func(t *testing.T) {
			dir := t.TempDir()
			copyDir(t, base, dir)
			injected := errors.New("injected failure")
			injectAt(t, step, injected)
			res, err := PatchMonth(ctx, opts, dir, sep, p, false)
			if committed(step) {
				// committed: the recovery completed the publication
				if err != nil {
					t.Fatalf("err = %v, want the publication completed", err)
				}
				if !reflect.DeepEqual(res.Entry.Files, want) {
					t.Errorf("entry = %+v", res.Entry.Files)
				}
				checkMonthState(t, opts, dir, sep, want)
				return
			}
			if !errors.Is(err, injected) {
				t.Fatalf("err = %v, want the injected failure", err)
			}
			checkMonthState(t, opts, dir, sep, old.Files)
		})

		t.Run("crash at "+step, func(t *testing.T) {
			dir := t.TempDir()
			copyDir(t, base, dir)
			injectAt(t, step, errSimulatedCrash)
			if _, err := PatchMonth(ctx, opts, dir, sep, p, false); !errors.Is(err, errSimulatedCrash) {
				t.Fatalf("err = %v, want the simulated crash", err)
			}
			testHookPublishStep = nil
			if step != "staged" {
				m, err := ReadManifest(filepath.Join(dir, ManifestFile))
				if err != nil {
					t.Fatal(err)
				}
				if err := VerifyMonth(ctx, opts, dir, m, sep, false); err == nil || !strings.Contains(err.Error(), "coldstore-recover") {
					t.Errorf("verify of a stopped patch = %v, want a refusal", err)
				}
				if _, err := PatchMonth(ctx, opts, dir, sep, p, true); err == nil || !strings.Contains(err.Error(), "stopped partway") {
					t.Errorf("dry run on a stopped patch = %v, want a refusal", err)
				}
			}

			done, err := RecoverPatches(dir, []string{sep.Key})
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case step == "staged":
				if done[sep.Key] != RecoveryNone {
					t.Errorf("recovery = %q, want none (no journal yet)", done[sep.Key])
				}
				checkMonthState(t, opts, dir, sep, old.Files)
			case !committed(step):
				if done[sep.Key] != RecoveryRolledBack {
					t.Errorf("recovery = %q, want rolled back", done[sep.Key])
				}
				checkMonthState(t, opts, dir, sep, old.Files)
			default:
				if done[sep.Key] != RecoveryRolledForward {
					t.Errorf("recovery = %q, want rolled forward", done[sep.Key])
				}
				checkMonthState(t, opts, dir, sep, want)
				return
			}
			// rolled back: the patch applies again
			res, err := PatchMonth(ctx, opts, dir, sep, p, false)
			if err != nil || res.Entry == nil || !reflect.DeepEqual(res.Entry.Files, want) {
				t.Fatalf("patch after the recovery = %+v, %v", res, err)
			}
			checkMonthState(t, opts, dir, sep, want)
		})
	}

	// what coldstore-patch does when it is run again after a delete patch stopped after its commit: the reports are
	// gone from the manifest's entry, so the month must be recovered before they are located, or they would be in no
	// month and the month left half swapped
	t.Run("rerun of a stopped delete patch recovers", func(t *testing.T) {
		dir := t.TempDir()
		copyDir(t, base, dir)
		pd := Patch{Delete: []int64{
			pickReport(t, d, sep, used, func(r testReport) bool { return r.stage != testRecruitStageID }),
		}}
		wantDeleted := writeTestMonth(t, applyTestPatch(d, pd), t.TempDir(), sep).Files
		injectAt(t, "swapped "+FilePath(KindDropReports, sep.Key), errSimulatedCrash)
		if _, err := PatchMonth(ctx, opts, dir, sep, pd, false); !errors.Is(err, errSimulatedCrash) {
			t.Fatalf("err = %v, want the simulated crash", err)
		}
		testHookPublishStep = nil

		if _, err := PreparePatch(dir, nil, true); err == nil || !strings.Contains(err.Error(), "stopped partway") {
			t.Errorf("dry run preparation = %v, want a refusal", err)
		}
		if _, err := PreparePatch(dir, []string{"2026-08"}, true); err != nil {
			t.Errorf("dry run preparation of another month = %v", err)
		}
		if _, err := RebuildManifest(ctx, opts, dir, true); err == nil || !strings.Contains(err.Error(), "stopped partway") {
			t.Errorf("manifest rebuild = %v, want a refusal", err)
		}
		done, err := PreparePatch(dir, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if done[sep.Key] != RecoveryRolledForward {
			t.Errorf("recovery = %q, want rolled forward", done[sep.Key])
		}
		m, err := ReadManifest(filepath.Join(dir, ManifestFile))
		if err != nil {
			t.Fatal(err)
		}
		found, err := LocateReports(ctx, opts, dir, m, nil, pd.IDs())
		if err != nil || len(found) != 0 {
			t.Errorf("located = %v, %v; want the deleted report in no month", found, err)
		}
		checkMonthState(t, opts, dir, sep, wantDeleted)
	})

	t.Run("next patch recovers", func(t *testing.T) {
		dir := t.TempDir()
		copyDir(t, base, dir)
		injectAt(t, "swapped "+FilePath(KindExtras, sep.Key), errSimulatedCrash)
		if _, err := PatchMonth(ctx, opts, dir, sep, p, false); !errors.Is(err, errSimulatedCrash) {
			t.Fatalf("err = %v, want the simulated crash", err)
		}
		testHookPublishStep = nil
		// the same patch again: the stopped one is completed first, then there is nothing left to change
		res, err := PatchMonth(ctx, opts, dir, sep, p, false)
		if err != nil {
			t.Fatal(err)
		}
		if res.Entry != nil || res.Updated != 0 || len(res.Found) != 1 {
			t.Errorf("second patch = %+v, want a no-op", res)
		}
		checkMonthState(t, opts, dir, sep, want)
	})

	t.Run("new file lost after the commit", func(t *testing.T) {
		dir := t.TempDir()
		copyDir(t, base, dir)
		injectAt(t, "swapped "+FilePath(KindDropReports, sep.Key), errSimulatedCrash)
		if _, err := PatchMonth(ctx, opts, dir, sep, p, false); !errors.Is(err, errSimulatedCrash) {
			t.Fatalf("err = %v, want the simulated crash", err)
		}
		testHookPublishStep = nil
		// the generation cannot be completed: the previous one, kept whole, comes back, manifest entry included
		if err := os.Remove(nextName(dir, FilePath(KindRollup, sep.Key))); err != nil {
			t.Fatal(err)
		}
		done, err := RecoverPatches(dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if done[sep.Key] != RecoveryRolledBack {
			t.Errorf("recovery = %q, want rolled back", done[sep.Key])
		}
		checkMonthState(t, opts, dir, sep, old.Files)
	})

	t.Run("manifest cannot be written", func(t *testing.T) {
		dir := t.TempDir()
		copyDir(t, base, dir)
		// the manifest's lock cannot be opened: the update fails before the commit
		lock := filepath.Join(dir, ManifestFile+".lock")
		_ = os.Remove(lock)
		if err := os.Mkdir(lock, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := PatchMonth(ctx, opts, dir, sep, p, false); err == nil || !strings.Contains(err.Error(), "manifest lock") {
			t.Fatalf("err = %v, want the manifest's lock", err)
		}
		// the recovery only reads the manifest: the previous generation is back already
		if err := os.Remove(lock); err != nil {
			t.Fatal(err)
		}
		checkMonthState(t, opts, dir, sep, old.Files)
		res, err := PatchMonth(ctx, opts, dir, sep, p, false)
		if err != nil || res.Entry == nil {
			t.Fatalf("patch once the manifest can be written = %+v, %v", res, err)
		}
		checkMonthState(t, opts, dir, sep, want)
	})
}
