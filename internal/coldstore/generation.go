package coldstore

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// A patched month replaces files that may be the only copy of the month, so it is published as a new generation
// that can always be completed or undone from the directory alone, without PostgreSQL:
//
//  1. the new files are written and checked next to the old ones, then moved to <file>.next;
//  2. a journal (JournalDir/month-<key>.json) records the month's old and new entries;
//  3. the manifest's entry of the month is replaced by the new one: this is the commit point;
//  4. every old file is kept as <file>.prev (a hard link) and <file>.next is renamed over it;
//  5. the .prev files are removed, then the journal.
//
// If this stops at any point, recoverTarget (run before any later publication of the month, and by
// coldstore-recover) reads the journal and the manifest: an entry still old means the patch did not commit, and the
// old files are put back (from .prev where they were already replaced) and the .next files removed; an entry that
// is new means it did, and the remaining .next files are moved into place. Every file is compared with the
// journal's entries by size and SHA-256 before it is used.

// JournalDir is the directory, in a cold storage directory, of the journals of the patches being published.
const JournalDir = ".journal"

// journal is what a patch being published records before the manifest is changed.
type journal struct {
	Target string     `json:"target"`
	Old    MonthEntry `json:"old"`
	New    MonthEntry `json:"new"`
}

// Recovery is what recoverTarget did.
type Recovery string

const (
	// RecoveryNone: there was nothing to recover.
	RecoveryNone Recovery = ""
	// RecoveryRolledBack: the patch had not committed; the previous files are in place again.
	RecoveryRolledBack Recovery = "rolled back"
	// RecoveryRolledForward: the patch had committed; its files are now all in place.
	RecoveryRolledForward Recovery = "rolled forward"
)

// errSimulatedCrash, returned by testHookPublishStep, stops a publication as a crash would: nothing more is done,
// not even the recovery that follows an ordinary error.
var errSimulatedCrash = errors.New("coldstore: simulated crash")

// testHookPublishStep, if set, is called after each step of publishGeneration; an error from it fails the step.
var testHookPublishStep func(step string) error

func publishStep(step string) error {
	if testHookPublishStep == nil {
		return nil
	}
	return testHookPublishStep(step)
}

func journalPath(dir, target string) string {
	return filepath.Join(dir, JournalDir, target+".json")
}

func nextName(dir, path string) string { return filepath.Join(dir, filepath.FromSlash(path)) + ".next" }
func prevName(dir, path string) string { return filepath.Join(dir, filepath.FromSlash(path)) + ".prev" }

// publishGeneration replaces the files of an archived month (old, its entry in the manifest) with the staged ones
// and records them in the manifest, recoverably (see above). The caller holds the month's lock. When a step fails
// after the journal is written, the publication is recovered at once: it returns nil if the month ended up with the
// new files (the manifest had committed), and the error otherwise.
func publishGeneration(dir string, old *MonthEntry, s *stagedMonth) error {
	target := MonthTarget(old.Month)
	jr := journal{Target: target, Old: *old, New: *s.entry}
	if err := stageNext(dir, &jr, s); err != nil {
		if !errors.Is(err, errSimulatedCrash) {
			_ = removeStrayNext(dir, target)
		}
		return err
	}

	err := commitGeneration(dir, &jr)
	if err == nil || errors.Is(err, errSimulatedCrash) {
		return err
	}
	rec, rerr := recoverTarget(dir, target)
	switch {
	case rerr != nil:
		return errors.Wrapf(err, "coldstore: publication of %s failed, and so did its recovery (%v); run coldstore-recover", old.Month, rerr)
	case rec == RecoveryRolledForward:
		log.Warn().Err(err).Str("month", old.Month).Msg("coldstore: publication failed after its commit, completed by recovery")
		return nil
	default:
		return errors.Wrapf(err, "coldstore: publication of %s failed, previous files kept", old.Month)
	}
}

// stageNext moves the staged files to their .next names and writes the journal.
func stageNext(dir string, jr *journal, s *stagedMonth) error {
	for _, fe := range jr.New.Files {
		kind, ok := kindOf(fe.Path, jr.New.Month)
		if !ok {
			return fmt.Errorf("coldstore: unexpected file %s in the new entry", fe.Path)
		}
		if err := os.Rename(s.tmp[kind], nextName(dir, fe.Path)); err != nil {
			return errors.Wrap(err, "coldstore: stage")
		}
		delete(s.tmp, kind)
	}
	if err := syncDir(dir, jr.New.Files); err != nil {
		return err
	}
	if err := publishStep("staged"); err != nil {
		return err
	}
	return writeJournal(dir, jr)
}

// commitGeneration is publishGeneration once the journal is written: the manifest, then the files.
func commitGeneration(dir string, jr *journal) error {
	if err := publishStep("journaled"); err != nil {
		return err
	}
	key := jr.Old.Month
	err := UpdateManifest(filepath.Join(dir, ManifestFile), func(m *Manifest) error {
		if !sameFiles(m.Month(key), &jr.Old) {
			return fmt.Errorf("coldstore: the manifest's entry of %s changed during the patch", key)
		}
		m.PutMonth(jr.New)
		return nil
	})
	if err != nil {
		return err
	}
	if err := publishStep("committed"); err != nil {
		return err
	}
	for i, fe := range jr.New.Files {
		if err := swapIn(dir, fe.Path); err != nil {
			return err
		}
		if err := syncDir(dir, jr.New.Files[i:i+1]); err != nil {
			return err
		}
		if err := publishStep("swapped " + fe.Path); err != nil {
			return err
		}
	}
	return finishJournal(dir, jr)
}

// swapIn keeps a file as its .prev (unless already kept) and renames its .next over it.
func swapIn(dir, path string) error {
	final := filepath.Join(dir, filepath.FromSlash(path))
	if _, err := os.Lstat(prevName(dir, path)); errors.Is(err, os.ErrNotExist) {
		if err := linkOrCopy(final, prevName(dir, path)); err != nil {
			return err
		}
	} else if err != nil {
		return errors.Wrap(err, "coldstore: stat")
	}
	return errors.Wrap(os.Rename(nextName(dir, path), final), "coldstore: rename")
}

// finishJournal removes the .prev and .next files of a publication whose month is complete, then its journal.
func finishJournal(dir string, jr *journal) error {
	for _, fe := range append(append([]FileEntry{}, jr.Old.Files...), jr.New.Files...) {
		for _, name := range []string{prevName(dir, fe.Path), nextName(dir, fe.Path)} {
			if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
				return errors.Wrap(err, "coldstore: remove")
			}
		}
	}
	if err := syncDir(dir, jr.New.Files); err != nil {
		return err
	}
	if err := publishStep("cleaned"); err != nil {
		return err
	}
	if err := os.Remove(journalPath(dir, jr.Target)); err != nil {
		return errors.Wrap(err, "coldstore: remove journal")
	}
	return syncDir(dir, []FileEntry{{Path: JournalDir + "/" + jr.Target + ".json"}})
}

// recoverTarget completes or undoes the publication of a target that stopped partway (see above), from its journal
// and the manifest. The caller holds the target's lock. Without a journal it only removes the .next files a
// publication left before writing its journal.
func recoverTarget(dir, target string) (Recovery, error) {
	b, err := os.ReadFile(journalPath(dir, target))
	if errors.Is(err, os.ErrNotExist) {
		return RecoveryNone, removeStrayNext(dir, target)
	}
	if err != nil {
		return RecoveryNone, errors.Wrap(err, "coldstore: read journal")
	}
	var jr journal
	if err := json.Unmarshal(b, &jr); err != nil || jr.Target != target || jr.Old.Month == "" || jr.Old.Month != jr.New.Month {
		return RecoveryNone, fmt.Errorf("coldstore: invalid journal %s: %v", journalPath(dir, target), err)
	}
	m, err := ReadManifest(filepath.Join(dir, ManifestFile))
	if err != nil {
		return RecoveryNone, err
	}
	cur := m.Month(jr.Old.Month)
	switch {
	case sameFiles(cur, &jr.New):
		if err := rollForward(dir, &jr); err == nil {
			return RecoveryRolledForward, finishJournal(dir, &jr)
		} else if !canRestore(dir, &jr) {
			return RecoveryNone, err
		}
		// a new file is lost: the old generation is still whole, go back to it, manifest included
		log.Warn().Err(err).Str("target", target).Msg("coldstore: cannot complete the publication, restoring the previous files")
		if err := restoreOld(dir, &jr); err != nil {
			return RecoveryNone, err
		}
		if err := UpdateManifest(filepath.Join(dir, ManifestFile), func(m *Manifest) error {
			if !sameFiles(m.Month(jr.Old.Month), &jr.New) {
				return fmt.Errorf("coldstore: the manifest's entry of %s changed during the recovery", jr.Old.Month)
			}
			m.PutMonth(jr.Old)
			return nil
		}); err != nil {
			return RecoveryNone, err
		}
		return RecoveryRolledBack, finishJournal(dir, &jr)
	case sameFiles(cur, &jr.Old):
		if err := restoreOld(dir, &jr); err != nil {
			return RecoveryNone, err
		}
		return RecoveryRolledBack, finishJournal(dir, &jr)
	default:
		return RecoveryNone, fmt.Errorf("coldstore: the manifest's entry of %s is neither the old nor the new one of the journal %s: "+
			"recover it by hand", jr.Old.Month, journalPath(dir, target))
	}
}

// rollForward moves the new files that are not in place yet into place.
func rollForward(dir string, jr *journal) error {
	for _, fe := range jr.New.Files {
		if ok, err := fileIs(filepath.Join(dir, filepath.FromSlash(fe.Path)), fe); err != nil || ok {
			if err != nil {
				return err
			}
			continue
		}
		if ok, err := fileIs(nextName(dir, fe.Path), fe); err != nil || !ok {
			if err == nil {
				err = fmt.Errorf("coldstore: neither %s nor its .next is the new file", fe.Path)
			}
			return err
		}
		if err := swapIn(dir, fe.Path); err != nil {
			return err
		}
	}
	return syncDir(dir, jr.New.Files)
}

// canRestore tells whether every old file is in place or kept as its .prev.
func canRestore(dir string, jr *journal) bool {
	for _, fe := range jr.Old.Files {
		inPlace, _ := fileIs(filepath.Join(dir, filepath.FromSlash(fe.Path)), fe)
		kept, _ := fileIs(prevName(dir, fe.Path), fe)
		if !inPlace && !kept {
			return false
		}
	}
	return true
}

// restoreOld puts back the old files that were replaced, from their .prev.
func restoreOld(dir string, jr *journal) error {
	for _, fe := range jr.Old.Files {
		final := filepath.Join(dir, filepath.FromSlash(fe.Path))
		if ok, err := fileIs(final, fe); err != nil || ok {
			if err != nil {
				return err
			}
			continue
		}
		if ok, err := fileIs(prevName(dir, fe.Path), fe); err != nil || !ok {
			if err == nil {
				err = fmt.Errorf("coldstore: neither %s nor its .prev is the previous file: recover it by hand", fe.Path)
			}
			return err
		}
		if err := os.Rename(prevName(dir, fe.Path), final); err != nil {
			return errors.Wrap(err, "coldstore: rename")
		}
	}
	return syncDir(dir, jr.Old.Files)
}

// removeStrayNext removes the .next files of a month target that has no journal.
func removeStrayNext(dir, target string) error {
	key, ok := strings.CutPrefix(target, MonthTarget(""))
	if !ok {
		return nil
	}
	for _, kind := range Kinds {
		if err := os.Remove(nextName(dir, FilePath(kind, key))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.Wrap(err, "coldstore: remove")
		}
	}
	return nil
}

// fileIs tells whether a file has the size and SHA-256 of an entry; a missing file is not.
func fileIs(name string, fe FileEntry) (bool, error) {
	f, err := os.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errors.Wrap(err, "coldstore: open")
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.Size() != fe.Bytes {
		return false, errors.Wrap(err, "coldstore: stat")
	}
	got, err := hashFile(f)
	return err == nil && got.SHA256 == fe.SHA256, err
}

// sameFiles tells whether a manifest entry (nil if none) lists the same files as another.
func sameFiles(e, other *MonthEntry) bool {
	if e == nil || len(e.Files) != len(other.Files) {
		return false
	}
	for i := range e.Files {
		a, b := e.Files[i], other.Files[i]
		if a.Path != b.Path || a.SHA256 != b.SHA256 || a.Bytes != b.Bytes {
			return false
		}
	}
	return true
}

// kindOf returns the kind of a file of a month.
func kindOf(path, key string) (Kind, bool) {
	for _, kind := range Kinds {
		if FilePath(kind, key) == path {
			return kind, true
		}
	}
	return "", false
}

// writeJournal writes a journal atomically and durably.
func writeJournal(dir string, jr *journal) error {
	b, err := json.MarshalIndent(jr, "", "  ")
	if err != nil {
		return err
	}
	name := journalPath(dir, jr.Target)
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return errors.Wrap(err, "coldstore: create journal directory")
	}
	tmp := name + ".tmp"
	if err := writeFileSync(tmp, b); err != nil {
		return err
	}
	if err := os.Rename(tmp, name); err != nil {
		return errors.Wrap(err, "coldstore: rename journal")
	}
	return syncDir(dir, []FileEntry{{Path: JournalDir + "/" + jr.Target + ".json"}})
}

func writeFileSync(name string, b []byte) error {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return errors.Wrap(err, "coldstore: create")
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return errors.Wrap(err, "coldstore: write")
}

// linkOrCopy makes dst a hard link to src, or a durable copy of it where hard links are not supported.
func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return errors.Wrap(err, "coldstore: open")
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return errors.Wrap(err, "coldstore: create")
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return errors.Wrap(err, "coldstore: copy")
}

// RecoverPatches completes or undoes the patches of the given months (all the journals in dir if none) that
// stopped partway, holding each month's lock, and returns what was done per month.
func RecoverPatches(dir string, months []string) (map[string]Recovery, error) {
	if len(months) == 0 {
		var err error
		if months, err = journalMonths(dir); err != nil {
			return nil, err
		}
	}
	out := map[string]Recovery{}
	for _, key := range months {
		rec, err := func() (Recovery, error) {
			unlock, err := lockTarget(dir, MonthTarget(key))
			if err != nil {
				return RecoveryNone, err
			}
			defer unlock()
			return recoverTarget(dir, MonthTarget(key))
		}()
		if err != nil {
			return out, errors.Wrapf(err, "coldstore: recover %s", key)
		}
		out[key] = rec
	}
	return out, nil
}

// PendingPatch tells whether a patch of the month stopped partway (it has a journal).
func PendingPatch(dir, key string) bool {
	_, err := os.Stat(journalPath(dir, MonthTarget(key)))
	return err == nil
}

// journalMonths lists the months of dir that have a journal: a patch of them stopped partway.
func journalMonths(dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, JournalDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.Wrap(err, "coldstore: list journals")
	}
	var months []string
	for _, e := range entries {
		if key, ok := strings.CutSuffix(e.Name(), ".json"); ok {
			if key, ok = strings.CutPrefix(key, MonthTarget("")); ok {
				months = append(months, key)
			}
		}
	}
	return months, nil
}

// PreparePatch readies dir for a patch of the given months (of every month if none), before the months' reports
// are located (LocateReports): a patch of them that stopped partway is completed or undone first (see
// RecoverPatches), so that its months are whole again; a delete patch that stopped after its commit would otherwise
// look as if its reports were in no month. A dry run changes nothing: it refuses such a month instead.
func PreparePatch(dir string, months []string, dryRun bool) (map[string]Recovery, error) {
	if !dryRun {
		return RecoverPatches(dir, months)
	}
	pending, err := journalMonths(dir)
	if err != nil {
		return nil, err
	}
	if len(months) > 0 {
		var selected []string
		for _, key := range pending {
			if slices.Contains(months, key) {
				selected = append(selected, key)
			}
		}
		pending = selected
	}
	if len(pending) > 0 {
		return nil, fmt.Errorf("coldstore: a patch of %s stopped partway: run coldstore-recover (or the patch without --dry-run) first",
			strings.Join(pending, ", "))
	}
	return nil, nil
}
