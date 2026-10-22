package coldstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// publisher runs Publish in a goroutine, so that a test can interleave two of them: write is the function given to
// Publish, err its result once done is closed.
type publisher struct {
	done chan struct{}
	err  error
}

func startPublish(dir, target string, write func(m *Manifest) (func(*Manifest), error)) *publisher {
	p := &publisher{done: make(chan struct{})}
	go func() {
		defer close(p.done)
		p.err = Publish(dir, target, write)
	}()
	return p
}

func (p *publisher) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-p.done:
		return p.err
	case <-time.After(time.Minute):
		t.Fatal("publication did not finish")
		return nil
	}
}

func waitFor(t *testing.T, c <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(time.Minute):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// onLockWait sets testHookLockWait for the test: wait is closed when a publication of target finds it locked.
func onLockWait(t *testing.T, target string) (wait <-chan struct{}) {
	t.Helper()
	c := make(chan struct{})
	var once sync.Once
	testHookLockWait = func(got string) {
		if got != target {
			t.Errorf("publication of %s waited for its lock", got)
			return
		}
		once.Do(func() { close(c) })
	}
	t.Cleanup(func() { testHookLockWait = nil })
	return c
}

// replaceFile replaces a file of dir the way the exporter does (a temporary file renamed into place) and returns
// its entry.
func replaceFile(t *testing.T, dir, path, content string) FileEntry {
	t.Helper()
	final := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		t.Error(err)
	}
	if err := os.WriteFile(final+".tmp", []byte(content), 0o644); err != nil {
		t.Error(err)
	}
	if err := os.Rename(final+".tmp", final); err != nil {
		t.Error(err)
	}
	fe, err := describeExisting(dir, path)
	if err != nil {
		t.Error(err)
	}
	return fe
}

// checkManifestMatchesFiles checks that every file of dir's manifest has the size and SHA-256 recorded for it.
func checkManifestMatchesFiles(t *testing.T, dir string) *Manifest {
	t.Helper()
	m, err := ReadManifest(filepath.Join(dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	files := append([]FileEntry{}, m.Dims...)
	for _, e := range m.Months {
		files = append(files, e.Files...)
	}
	for _, want := range files {
		got, err := describeExisting(dir, want.Path)
		if err != nil {
			t.Fatal(err)
		}
		if got.SHA256 != want.SHA256 || got.Bytes != want.Bytes {
			t.Errorf("%s: %d bytes, sha256 %s; manifest: %d bytes, sha256 %s", want.Path, got.Bytes, got.SHA256, want.Bytes, want.SHA256)
		}
	}
	return m
}

// TestPublishSerializesSameTarget interleaves two publications of the dims: A replaces the file and pauses before
// its manifest update; B, started then, must wait for A's lock before it writes anything, so that the manifest ends
// up describing the file that is there (B's). Without the lock, B would replace the file and record it, then A
// would record its own, older file.
func TestPublishSerializesSameTarget(t *testing.T) {
	dir := t.TempDir()
	bWaits := onLockWait(t, DimsTarget)

	aWrote, releaseA := make(chan struct{}), make(chan struct{})
	a := startPublish(dir, DimsTarget, func(*Manifest) (func(*Manifest), error) {
		fe := replaceFile(t, dir, DropPatternElementsFile, "elements as A read them")
		close(aWrote)
		<-releaseA
		return func(m *Manifest) { m.PutDim(fe) }, nil
	})
	waitFor(t, aWrote, "A to replace the file")

	bWrote := make(chan struct{})
	var bSaw *FileEntry
	b := startPublish(dir, DimsTarget, func(m *Manifest) (func(*Manifest), error) {
		bSaw = m.Dim(DropPatternElementsFile)
		fe := replaceFile(t, dir, DropPatternElementsFile, "elements as B read them, one more")
		close(bWrote)
		return func(m *Manifest) { m.PutDim(fe) }, nil
	})
	waitFor(t, bWaits, "B to wait for the lock")
	select {
	case <-bWrote:
		t.Fatal("B replaced the file while A held the lock")
	default:
	}

	close(releaseA)
	if err := a.wait(t); err != nil {
		t.Fatal(err)
	}
	if err := b.wait(t); err != nil {
		t.Fatal(err)
	}
	if bSaw == nil {
		t.Error("B did not see A's entry in the manifest it was given")
	}
	m := checkManifestMatchesFiles(t, dir)
	if b, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(DropPatternElementsFile))); !strings.HasPrefix(string(b), "elements as B") {
		t.Errorf("file holds %q, want B's", b)
	}
	if len(m.Dims) != 1 {
		t.Errorf("dims = %+v", m.Dims)
	}
}

// TestPublishSameMonthChecksUnderLock interleaves two exports of the same month: B's check of the manifest (is the
// month archived?) must see A's entry, so B refuses instead of replacing A's files.
func TestPublishSameMonthChecksUnderLock(t *testing.T) {
	dir := t.TempDir()
	const key = "2026-01"
	target := MonthTarget(key)
	bWaits := onLockWait(t, target)

	write := func(content string, pause <-chan struct{}, wrote chan<- struct{}) func(*Manifest) (func(*Manifest), error) {
		return func(m *Manifest) (func(*Manifest), error) {
			if m.Month(key) != nil {
				return nil, &VerificationError{Subject: "month " + key, Problems: []string{"already archived"}}
			}
			e := MonthEntry{Month: key}
			for _, kind := range Kinds {
				e.Files = append(e.Files, replaceFile(t, dir, FilePath(kind, key), content+" "+string(kind)))
			}
			if wrote != nil {
				close(wrote)
			}
			if pause != nil {
				<-pause
			}
			return func(m *Manifest) { m.PutMonth(e) }, nil
		}
	}
	aWrote, releaseA := make(chan struct{}), make(chan struct{})
	a := startPublish(dir, target, write("A", releaseA, aWrote))
	waitFor(t, aWrote, "A to replace the files")
	b := startPublish(dir, target, write("B", nil, nil))
	waitFor(t, bWaits, "B to wait for the lock")
	close(releaseA)
	if err := a.wait(t); err != nil {
		t.Fatal(err)
	}
	if err := b.wait(t); err == nil || !strings.Contains(err.Error(), "already archived") {
		t.Errorf("B: err = %v, want already archived", err)
	}
	checkManifestMatchesFiles(t, dir)
	if b, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(FilePath(KindRollup, key)))); string(b) != "A rollup" {
		t.Errorf("rollup holds %q, want A's", b)
	}
}

// TestPublishDistinctMonthsConcurrently checks that a month's lock does not hold back another month.
func TestPublishDistinctMonthsConcurrently(t *testing.T) {
	dir := t.TempDir()
	testHookLockWait = func(target string) { t.Errorf("publication of %s waited for its lock", target) }
	t.Cleanup(func() { testHookLockWait = nil })

	month := func(key string, pause <-chan struct{}, wrote chan<- struct{}) func(*Manifest) (func(*Manifest), error) {
		return func(*Manifest) (func(*Manifest), error) {
			e := MonthEntry{Month: key}
			for _, kind := range Kinds {
				e.Files = append(e.Files, replaceFile(t, dir, FilePath(kind, key), key+" "+string(kind)))
			}
			if wrote != nil {
				close(wrote)
			}
			if pause != nil {
				<-pause
			}
			return func(m *Manifest) { m.PutMonth(e) }, nil
		}
	}
	aWrote, releaseA := make(chan struct{}), make(chan struct{})
	a := startPublish(dir, MonthTarget("2026-01"), month("2026-01", releaseA, aWrote))
	waitFor(t, aWrote, "2026-01 to replace its files")
	// 2026-02 completes while 2026-01 holds its lock
	if err := startPublish(dir, MonthTarget("2026-02"), month("2026-02", nil, nil)).wait(t); err != nil {
		t.Fatal(err)
	}
	close(releaseA)
	if err := a.wait(t); err != nil {
		t.Fatal(err)
	}
	if m := checkManifestMatchesFiles(t, dir); len(m.Months) != 2 {
		t.Errorf("%d months recorded, want 2", len(m.Months))
	}
}

// TestPublishDimsFromPostgres is TestPublishSerializesSameTarget with the exporter: A exports the drop pattern
// elements and pauses before recording them; a pattern element is added to PostgreSQL; B exports them again. The
// manifest must end up describing B's file, which passes VerifyDims against PostgreSQL.
func TestPublishDimsFromPostgres(t *testing.T) {
	ctx := context.Background()
	opts, pg := newTestPostgres(t, newTestData())
	dir := t.TempDir()
	bWaits := onLockWait(t, DimsTarget)

	export := func(wrote chan<- struct{}, pause <-chan struct{}) func(*Manifest) (func(*Manifest), error) {
		return func(*Manifest) (func(*Manifest), error) {
			fe, err := ExportDims(ctx, opts, dir, nil)
			if err != nil {
				return nil, err
			}
			if wrote != nil {
				close(wrote)
			}
			if pause != nil {
				<-pause
			}
			return func(m *Manifest) { m.PutDim(*fe) }, nil
		}
	}
	aWrote, releaseA := make(chan struct{}), make(chan struct{})
	a := startPublish(dir, DimsTarget, export(aWrote, releaseA))
	waitFor(t, aWrote, "A to export the dims")
	if _, err := pg.ExecContext(ctx, `INSERT INTO drop_pattern_elements (drop_pattern_id, item_id, quantity) VALUES (21, 7, 1)`); err != nil {
		t.Fatal(err)
	}
	b := startPublish(dir, DimsTarget, export(nil, nil))
	waitFor(t, bWaits, "B to wait for the lock")
	close(releaseA)
	if err := a.wait(t); err != nil {
		t.Fatal(err)
	}
	if err := b.wait(t); err != nil {
		t.Fatal(err)
	}
	m := checkManifestMatchesFiles(t, dir)
	if err := VerifyDims(ctx, opts, dir, m, true); err != nil {
		t.Fatal(err)
	}
	if got := m.Dim(DropPatternElementsFile).Rows; got != 61 {
		t.Errorf("dims rows = %d, want 61 (B's export)", got)
	}
}

// TestRebuildManifest checks that coldstore-manifest's rebuild of a directory's own manifest needs force to replace
// one, replaces an unreadable one, and waits for an export of a month that holds the month's lock: the manifest it
// writes then describes the files that export wrote, not those it would have indexed before them.
func TestRebuildManifest(t *testing.T) {
	ctx := context.Background()
	d := newTestData()
	sep := mustMonth(t, "2026-09")
	opts := Options{WorkDir: t.TempDir(), Threads: 2}
	dir := t.TempDir()
	name := filepath.Join(dir, ManifestFile)
	e := writeTestMonth(t, d, dir, sep)
	if err := UpdateManifest(name, func(m *Manifest) error { m.PutMonth(*e); return nil }); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RebuildManifest(ctx, opts, dir, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("rebuild over an existing manifest = %v, want a refusal", err)
	}
	if after, _ := os.ReadFile(name); string(after) != string(before) {
		t.Error("the refused rebuild changed the manifest")
	}

	// another generation of the month, exported once the rebuild has indexed the files: the export must wait for
	// the rebuild to write the manifest, or the rebuild would record the files it indexed over the export's entry
	d2 := *d
	d2.reports = append([]testReport{}, d.reports...)
	for i, r := range d2.reports {
		if d.inMonth(sep)(r) && r.stage != testRecruitStageID && r.reliability == 0 {
			d2.reports[i].reliability = -1
			break
		}
	}
	indexed, release := make(chan struct{}), make(chan struct{})
	testHookRebuildIndexed = func() {
		close(indexed)
		<-release
	}
	t.Cleanup(func() { testHookRebuildIndexed = nil })
	exportWaits := onLockWait(t, MonthTarget(sep.Key))
	rebuilt := make(chan error, 1)
	go func() {
		_, err := RebuildManifest(ctx, opts, dir, true)
		rebuilt <- err
	}()
	waitFor(t, indexed, "the rebuild to index the files")
	export := startPublish(dir, MonthTarget(sep.Key), func(*Manifest) (func(*Manifest), error) {
		e2 := writeTestMonth(t, &d2, dir, sep)
		return func(m *Manifest) { m.PutMonth(*e2) }, nil
	})
	waitFor(t, exportWaits, "the export to wait for the month's lock")
	close(release)
	select {
	case err := <-rebuilt:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Minute):
		t.Fatal("the rebuild did not finish")
	}
	if err := export.wait(t); err != nil {
		t.Fatal(err)
	}
	testHookRebuildIndexed = nil
	m := checkManifestMatchesFiles(t, dir)
	if err := VerifyMonth(ctx, opts, dir, m, sep, false); err != nil {
		t.Errorf("verify after the rebuild: %v", err)
	}

	// an unreadable manifest is replaced with force
	if err := os.WriteFile(name, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RebuildManifest(ctx, opts, dir, false); err == nil {
		t.Error("rebuild over an unreadable manifest succeeded without force")
	}
	if _, err := RebuildManifest(ctx, opts, dir, true); err != nil {
		t.Fatal(err)
	}
	checkManifestMatchesFiles(t, dir)
}

// TestRebuildManifestRechecksUnreadable interleaves two forced rebuilds of a directory whose manifest is unreadable
// with an export of a new month: A indexes the files and pauses; B finds the manifest unreadable too and waits for
// A's lock; A repairs the manifest; once B has indexed the files, August is exported and recorded in the repaired
// manifest. B must then see that the manifest is readable and refuse to drop August's entry, rather than replace it
// as the unreadable manifest it found at the start.
func TestRebuildManifestRechecksUnreadable(t *testing.T) {
	ctx := context.Background()
	d := newTestData()
	aug, sep := mustMonth(t, "2026-08"), mustMonth(t, "2026-09")
	opts := Options{WorkDir: t.TempDir(), Threads: 2}
	dir := t.TempDir()
	name := filepath.Join(dir, ManifestFile)
	writeTestMonth(t, d, dir, sep)
	if err := os.WriteFile(name, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	indexed, release := make(chan struct{}), make(chan struct{})
	testHookRebuildIndexed = func() {
		indexed <- struct{}{}
		<-release
	}
	t.Cleanup(func() { testHookRebuildIndexed = nil })
	rebuild := func() <-chan error {
		c := make(chan error, 1)
		go func() {
			_, err := RebuildManifest(ctx, opts, dir, true)
			c <- err
		}()
		return c
	}
	result := func(c <-chan error, what string) error {
		t.Helper()
		select {
		case err := <-c:
			return err
		case <-time.After(time.Minute):
			t.Fatalf("%s did not finish", what)
			return nil
		}
	}

	a := rebuild()
	waitFor(t, indexed, "rebuild A to index the files")
	bWaits := onLockWait(t, DimsTarget)
	b := rebuild()
	waitFor(t, bWaits, "rebuild B to wait for A's lock")
	release <- struct{}{}
	if err := result(a, "rebuild A"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(name); err != nil {
		t.Fatalf("rebuild A did not repair the manifest: %v", err)
	}
	waitFor(t, indexed, "rebuild B to index the files")

	export := startPublish(dir, MonthTarget(aug.Key), func(*Manifest) (func(*Manifest), error) {
		e := writeTestMonth(t, d, dir, aug)
		return func(m *Manifest) { m.PutMonth(*e) }, nil
	})
	if err := export.wait(t); err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	if err := result(b, "rebuild B"); err == nil || !strings.Contains(err.Error(), "run again") {
		t.Errorf("rebuild B = %v, want a refusal to drop the month exported meanwhile", err)
	}
	testHookRebuildIndexed = nil
	m := checkManifestMatchesFiles(t, dir)
	if m.Month(aug.Key) == nil || m.Month(sep.Key) == nil {
		t.Errorf("manifest has %d months, want 2026-08 and 2026-09", len(m.Months))
	}

	// once nothing runs concurrently, the rebuild records both months
	if _, err := RebuildManifest(ctx, opts, dir, true); err != nil {
		t.Fatal(err)
	}
	if m := checkManifestMatchesFiles(t, dir); len(m.Months) != 2 {
		t.Errorf("manifest has %d months after the rebuild, want 2", len(m.Months))
	}
}
