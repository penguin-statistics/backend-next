package coldstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/pkg/duckdbx"
)

// The app reads the cold storage from a directory on the local disk of the machine it runs on, never from the
// bucket: DuckDB is confined to that directory and its temp directory (see duckdbx.Config.AllowedDirectories), so
// a query cannot read anything over the network. When a file is missing or does not have its manifest's size, the
// app refuses to start (OpenReader) instead of reading it from elsewhere; the only way to get a file from the
// bucket is the manual run-script coldstore-pull (see Pull).
//
// A patch replaces the files of a month one after the other once its manifest entry is committed, and a pull or an
// export replaces files too, all under the month's exclusive lock (see Publish and publishGeneration), and the
// queries read the files by name, whatever View they took them from. So a query holds a shared lock on the month of
// every file it reads (QueryFiles), and on the dimensions, from before it starts until its rows are closed: it never
// reads the files of a month as two generations, and a publication waits for the queries that read its month. A
// month whose patch stopped partway (its journal is still there) may hold files of both generations: the queries
// that read it fail until coldstore-recover completes or undoes the patch.

// ReaderConfig configures a Reader.
type ReaderConfig struct {
	// Dir is the local cold storage directory, holding manifest.json and the files it lists.
	Dir string
	// Threads caps DuckDB's worker threads.
	Threads int
	// MemoryLimit caps DuckDB's memory, in DuckDB's syntax (e.g. "1GB").
	MemoryLimit string
	// TempDir is where the Reader creates a temp directory of its own, for DuckDB's spill files, removed on Close;
	// empty is the system's temp directory. It also keeps the time of the last integrity check
	// (IntegrityStateFile), so that a restart does not start the weekly check over.
	TempDir string
	// ReloadInterval is how often the manifest is checked for a change (a month archived or patched); 0 never.
	ReloadInterval time.Duration
	// IntegrityCheckInterval is how often every file is hashed again and compared with the manifest's SHA-256
	// (CheckIntegrity); 0 never.
	IntegrityCheckInterval time.Duration
}

// readerConns is the size of the connection pool. Queries on one database share its threads; the pool only lets a
// few of them make progress at once.
const readerConns = 4

// Reader serves queries over the local cold storage with one long-lived DuckDB database. Use View to get the files
// and the hot boundary of the current manifest, and Query to run a query over them, naming them with
// QueryFiles.ReadParquet.
type Reader struct {
	cfg      ReaderConfig
	dir      string
	tempRoot string
	tempDir  string
	db       *sql.DB

	mu       sync.RWMutex
	view     *View
	loadedAt manifestStamp

	stop chan struct{}
	wg   sync.WaitGroup
	once sync.Once
}

// manifestStamp identifies a version of the manifest file, to notice that it was replaced.
type manifestStamp struct {
	size    int64
	modTime time.Time
}

// OpenReader opens the local cold storage: it reads the manifest, checks that it covers every month from the first
// one on without a gap (CheckCoverage) and that every file it lists is in the directory with its size
// (CheckLocalFiles), and opens the DuckDB database. Any of these failing is an error: the caller (the app's
// startup) must not run without the files. The directory's LocksDir must be writable: the queries create the lock
// files they take. The background reload and integrity check start with Start.
func OpenReader(cfg ReaderConfig) (_ *Reader, err error) {
	if cfg.Dir == "" {
		return nil, errors.New("coldstore: no cold storage directory")
	}
	dir, err := filepath.Abs(cfg.Dir)
	if err != nil {
		return nil, errors.Wrap(err, "coldstore: cold storage directory")
	}
	r := &Reader{cfg: cfg, dir: dir, stop: make(chan struct{})}
	view, stamp, err := r.load()
	if err != nil {
		return nil, err
	}
	r.view, r.loadedAt = view, stamp
	if err := os.MkdirAll(filepath.Join(dir, LocksDir), 0o755); err != nil {
		return nil, errors.Wrap(err, "coldstore: create lock directory")
	}

	r.tempRoot = cfg.TempDir
	if r.tempRoot == "" {
		r.tempRoot = os.TempDir()
	}
	if r.tempRoot, err = filepath.Abs(r.tempRoot); err != nil {
		return nil, errors.Wrap(err, "coldstore: temp directory")
	}
	if err := os.MkdirAll(r.tempRoot, 0o755); err != nil {
		return nil, errors.Wrap(err, "coldstore: create temp directory")
	}
	// a directory of its own: DuckDB processes that shared a temp directory silently corrupted their output, and
	// two app processes can run on one machine during a deployment
	if r.tempDir, err = os.MkdirTemp(r.tempRoot, "coldstore-reader-"); err != nil {
		return nil, errors.Wrap(err, "coldstore: create temp directory")
	}
	// DuckDB checks the paths it writes once their links are resolved, as for the files it reads
	if resolved, err := filepath.EvalSymlinks(r.tempDir); err == nil {
		r.tempDir = resolved
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(r.tempDir)
		}
	}()

	r.db, err = duckdbx.Open(duckdbx.Config{
		Threads:              cfg.Threads,
		MemoryLimit:          cfg.MemoryLimit,
		TempDirectory:        r.tempDir,
		TimeZone:             "UTC",
		ParquetMetadataCache: true,
		AllowedDirectories:   allowedDirectories(dir),
	})
	if err != nil {
		return nil, errors.Wrap(err, "coldstore: open duckdb")
	}
	r.db.SetMaxOpenConns(readerConns)
	r.db.SetMaxIdleConns(readerConns)

	metricHotBoundary.Set(unixSeconds(view.boundary))
	log.Info().
		Str("evt.name", "coldstore.reader.open").
		Str("dir", dir).
		Int("months", len(view.manifest.Months)).
		Time("hotBoundary", view.boundary).
		Msg("coldstore: opened the local cold storage")
	return r, nil
}

// allowedDirectories returns the directories DuckDB may read: dir, and where dir and its subdirectories lead when
// they are symbolic links (see Manifest), since DuckDB checks the paths it opens once their links are resolved. The
// links are resolved once, when the database is opened.
func allowedDirectories(dir string) []string {
	dirs := []string{dir}
	subs := []string{filepath.Dir(filepath.FromSlash(DropPatternElementsFile))}
	for _, kind := range Kinds {
		subs = append(subs, string(kind))
	}
	for _, d := range append([]string{dir}, subs...) {
		if !filepath.IsAbs(d) {
			d = filepath.Join(dir, d)
		}
		if resolved, err := filepath.EvalSymlinks(d); err == nil && resolved != d {
			dirs = append(dirs, resolved)
		}
	}
	return dirs
}

// Dir returns the absolute path of the cold storage directory.
func (r *Reader) Dir() string {
	return r.dir
}

// View returns the current manifest's view of the cold storage. A query must take its files and its split of the
// time range from one View, so that a reload in between cannot make it miss or double count a month. The files may be
// of a later generation than the View describes (a month patched since), never of two: see Query.
func (r *Reader) View() *View {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.view
}

// QueryFiles collects the files a query reads, as it names them, for Reader.Query to lock: name every file of the
// cold storage in a query with ReadParquet, and pass the same QueryFiles to Query.
type QueryFiles struct {
	paths []string
}

// ReadParquet returns a read_parquet call over an explicit list of files (from a View), as the queries must name
// them: never a glob, which would read whatever files are in the directory instead of the manifest's. It panics on
// an empty list, which DuckDB rejects: check the list first.
func (f *QueryFiles) ReadParquet(files []string) string {
	f.paths = append(f.paths, files...)
	return readParquet(files)
}

// Query runs a query on the cold storage database, over the files of files (nil if none). It waits, until ctx ends,
// for the publications of their months (and dimensions) that are running, and fails if one stopped partway; it
// holds their locks until the rows are closed or read to the end.
func (r *Reader) Query(ctx context.Context, files *QueryFiles, query string, args ...any) (*Rows, error) {
	release, err := r.lockFiles(ctx, files)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		release()
		return nil, err
	}
	return &Rows{Rows: rows, release: release}, nil
}

// QueryRow is Query for a query that returns at most one row. The locks are held until Scan is called.
func (r *Reader) QueryRow(ctx context.Context, files *QueryFiles, query string, args ...any) *Row {
	rows, err := r.Query(ctx, files, query, args...)
	return &Row{rows: rows, err: err}
}

// Rows are the rows of Reader.Query. They hold the locks of the query's files until they are closed or read to the
// end: close them.
type Rows struct {
	*sql.Rows
	release func()
	once    sync.Once
}

// Next is sql.Rows.Next; the locks are released after the last row.
func (r *Rows) Next() bool {
	if r.Rows.Next() {
		return true
	}
	r.once.Do(r.release)
	return false
}

// Close is sql.Rows.Close, and releases the locks.
func (r *Rows) Close() error {
	err := r.Rows.Close()
	r.once.Do(r.release)
	return err
}

// Row is the row of Reader.QueryRow.
type Row struct {
	rows *Rows
	err  error
}

// Scan is sql.Row.Scan, and releases the locks.
func (r *Row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	defer r.rows.Close()
	if !r.rows.Next() {
		if err := r.rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	if err := r.rows.Scan(dest...); err != nil {
		return err
	}
	return r.rows.Close()
}

// readLockMaxWait is the longest a query sleeps between two attempts at the lock of a month being published.
const readLockMaxWait = 100 * time.Millisecond

// lockFiles takes a shared lock on the target (month or dimensions) of each of files, in the order of LockTargets,
// and checks that no publication of them stopped partway; it returns their release. A target being published is
// tried again until ctx ends: flock cannot wait for a context, and a goroutine blocked in it holds a thread.
func (r *Reader) lockFiles(ctx context.Context, files *QueryFiles) (_ func(), err error) {
	if files == nil || len(files.paths) == 0 {
		return func() {}, nil
	}
	set := make(map[string]bool)
	for _, p := range files.paths {
		rel, err := filepath.Rel(r.dir, p)
		target, ok := "", false
		if err == nil {
			target, ok = targetOfFile(filepath.ToSlash(rel))
		}
		if !ok {
			return nil, fmt.Errorf("coldstore: %s is not a file of the cold storage %s", p, r.dir)
		}
		set[target] = true
	}
	targets := make([]string, 0, len(set))
	for target := range set {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, k int) bool { return targetLess(targets[i], targets[k]) })

	var locks []*os.File
	release := func() {
		for i := len(locks) - 1; i >= 0; i-- {
			_ = syscall.Flock(int(locks[i].Fd()), syscall.LOCK_UN)
			locks[i].Close()
		}
	}
	defer func() {
		if err != nil {
			release()
		}
	}()
	for _, target := range targets {
		f, err := os.OpenFile(filepath.Join(r.dir, LocksDir, target+".lock"), os.O_RDONLY|os.O_CREATE, 0o644)
		if err != nil {
			return nil, errors.Wrap(err, "coldstore: open lock")
		}
		locks = append(locks, f)
		if err := lockShared(ctx, f, target); err != nil {
			return nil, err
		}
	}
	// a journal written under the lock that is still there: its publication stopped partway
	journals, err := os.ReadDir(filepath.Join(r.dir, JournalDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.Wrap(err, "coldstore: list journals")
	}
	for _, e := range journals {
		if target, ok := strings.CutSuffix(e.Name(), ".json"); ok && set[target] {
			key := strings.TrimPrefix(target, MonthTarget(""))
			return nil, fmt.Errorf("coldstore: a patch of %s stopped partway, its files may be of two generations; "+
				"complete or undo it with: backend run-script coldstore-recover --dir %s --month %s", key, r.dir, key)
		}
	}
	return release, nil
}

// testHookSharedLockWait, if set, is called when lockShared first finds its target locked, before it waits.
var testHookSharedLockWait func(target string)

// lockShared takes a shared lock on f, trying again while a publication holds it until ctx ends.
func lockShared(ctx context.Context, f *os.File, target string) error {
	for wait := time.Millisecond; ; wait = min(2*wait, readLockMaxWait) {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return errors.Wrapf(err, "coldstore: lock %s", target)
		}
		if wait == time.Millisecond && testHookSharedLockWait != nil {
			testHookSharedLockWait(target)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return errors.Wrapf(ctx.Err(), "coldstore: %s is being published", target)
		case <-t.C:
		}
	}
}

// targetOfFile returns the publication target of a file of the cold storage directory (a path relative to it).
func targetOfFile(rel string) (string, bool) {
	dir, name := path.Split(rel)
	if dir == path.Dir(DropPatternElementsFile)+"/" {
		return DimsTarget, true
	}
	for _, kind := range Kinds {
		if dir == string(kind)+"/" {
			if key, ok := monthOfFile(kind, name); ok {
				return MonthTarget(key), true
			}
		}
	}
	return "", false
}

// targetLess orders targets as LockTargets wants them: DimsTarget, then the months in order.
func targetLess(a, b string) bool {
	if a == DimsTarget || b == DimsTarget {
		return a == DimsTarget && b != DimsTarget
	}
	return monthLess(strings.TrimPrefix(a, MonthTarget("")), strings.TrimPrefix(b, MonthTarget("")))
}

// Start starts the background reload of the manifest and the integrity check, as configured.
func (r *Reader) Start() {
	if r.cfg.ReloadInterval > 0 {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.reloadLoop(r.cfg.ReloadInterval)
		}()
	}
	if r.cfg.IntegrityCheckInterval > 0 {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.integrityLoop(r.cfg.IntegrityCheckInterval)
		}()
	}
}

// Close stops the background work, closes the database once the queries running on it end, and removes the temp
// directory.
func (r *Reader) Close() error {
	var err error
	r.once.Do(func() {
		close(r.stop)
		r.wg.Wait()
		err = r.db.Close()
		if rerr := os.RemoveAll(r.tempDir); err == nil {
			err = rerr
		}
	})
	return err
}

// Reload reads the manifest again if it changed since it was loaded. A manifest that fails the checks of
// OpenReader is not taken: the previous view stays, and the error is returned (the background reload logs it,
// counts it and tries again on the next interval).
func (r *Reader) Reload() (changed bool, err error) {
	st, err := os.Stat(filepath.Join(r.dir, ManifestFile))
	if err != nil {
		return false, errors.Wrap(err, "coldstore: stat manifest")
	}
	r.mu.RLock()
	same := r.loadedAt == manifestStamp{size: st.Size(), modTime: st.ModTime()}
	r.mu.RUnlock()
	if same {
		return false, nil
	}
	view, stamp, err := r.load()
	if err != nil {
		return false, err
	}
	r.mu.Lock()
	old := r.view
	r.view, r.loadedAt = view, stamp
	r.mu.Unlock()
	metricHotBoundary.Set(unixSeconds(view.boundary))
	log.Info().
		Str("evt.name", "coldstore.reader.reload").
		Int("months", len(view.manifest.Months)).
		Time("hotBoundary", view.boundary).
		Time("previousHotBoundary", old.boundary).
		Msg("coldstore: reloaded the manifest")
	return true, nil
}

func (r *Reader) reloadLoop(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
		}
		if _, err := r.Reload(); err != nil {
			metricReloadFailures.Inc()
			log.Error().
				Err(err).
				Str("evt.name", "coldstore.reader.reload_failed").
				Msg("coldstore: the changed manifest cannot be served; still serving the previous one")
		}
	}
}

// load reads and checks the manifest. The stamp is taken before the manifest is read, so that a manifest replaced
// while it is read is read again by the next Reload.
func (r *Reader) load() (*View, manifestStamp, error) {
	name := filepath.Join(r.dir, ManifestFile)
	st, err := os.Stat(name)
	if err != nil {
		return nil, manifestStamp{}, errors.Wrapf(err, "coldstore: no manifest in the cold storage directory %s", r.dir)
	}
	m, err := ReadManifest(name)
	if err != nil {
		return nil, manifestStamp{}, err
	}
	if err := CheckCoverage(m); err != nil {
		return nil, manifestStamp{}, err
	}
	if err := CheckLocalFiles(r.dir, m); err != nil {
		return nil, manifestStamp{}, err
	}
	return newView(r.dir, m), manifestStamp{size: st.Size(), modTime: st.ModTime()}, nil
}

// HotBoundary returns the first instant after the last archived month of m, or the zero time when m has no month.
//
// The hot database holds the reports from the hot boundary on, the cold storage those before it. After the
// migration, the hot database's earliest partition starts at the hot boundary: a month's partition is dropped only
// after the month is in the manifest the app reads. Before the migration (shadow mode on the old database), the
// database still holds every month, so it simply has more data than the queries split at the boundary read from it.
func HotBoundary(m *Manifest) time.Time {
	if len(m.Months) == 0 {
		return time.Time{}
	}
	last, err := ParseMonth(m.Months[len(m.Months)-1].Month)
	if err != nil {
		return time.Time{}
	}
	return last.End
}

// CheckCoverage checks that m can serve every report before its hot boundary: its months are "pre" and every
// month from the first regular one (Epoch) to the last, without a gap, each with all its files, and the drop
// pattern elements. A gap would silently leave its month out of every query: it is neither cold nor hot.
func CheckCoverage(m *Manifest) error {
	if len(m.Months) == 0 {
		return nil
	}
	var problems []string
	want := PreMonth
	next := Epoch
	for _, e := range m.Months {
		if e.Month != want {
			problems = append(problems, fmt.Sprintf("month %s is missing (the next month in the manifest is %s)", want, e.Month))
			break
		}
		for _, kind := range Kinds {
			if e.File(kind) == nil {
				problems = append(problems, fmt.Sprintf("month %s has no %s file", e.Month, kind))
			}
		}
		want = next.Format("2006-01")
		next = next.AddDate(0, 1, 0)
	}
	if m.Dim(DropPatternElementsFile) == nil {
		problems = append(problems, DropPatternElementsFile+" is missing")
	}
	if len(problems) > 0 {
		return errors.New("coldstore: the manifest does not cover every month up to its last one: " + strings.Join(problems, "; "))
	}
	return nil
}

// LocalFilesError lists the files of the manifest that are not in the local cold storage directory as the
// manifest describes them.
type LocalFilesError struct {
	Dir      string
	Problems []string
}

func (e *LocalFilesError) Error() string {
	return "coldstore: the local cold storage " + e.Dir + " is incomplete, refusing to serve it (the app never reads the bucket): " +
		strings.Join(e.Problems, "; ")
}

// CheckLocalFiles checks, without reading them, that every file of m is in dir with the size the manifest gives,
// and that no patch stopped partway. Each problem names the month and the command that restores it from the bucket.
// A file must not itself be a symbolic link: DuckDB checks allowed_directories against the resolved path, and only
// the directory and its subdirectories are resolved for it (see allowedDirectories).
func CheckLocalFiles(dir string, m *Manifest) error {
	var problems []string
	check := func(fe FileEntry) []string {
		st, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(fe.Path)))
		switch {
		case errors.Is(err, os.ErrNotExist):
			return []string{fe.Path + " is missing"}
		case err != nil:
			return []string{fe.Path + ": " + err.Error()}
		case st.Mode()&os.ModeSymlink != 0:
			return []string{symlinkProblem(fe.Path)}
		case !st.Mode().IsRegular():
			return []string{fe.Path + " is not a regular file"}
		case st.Size() != fe.Bytes:
			return []string{fmt.Sprintf("%s has %d bytes, the manifest says %d", fe.Path, st.Size(), fe.Bytes)}
		}
		return nil
	}
	for _, fe := range m.Dims {
		if p := check(fe); len(p) > 0 {
			problems = append(problems, fmt.Sprintf("dims: %s; restore it with: %s", strings.Join(p, ", "), PullCommand(dir, "--dims")))
		}
	}
	for _, e := range m.Months {
		var p []string
		for _, fe := range e.Files {
			p = append(p, check(fe)...)
		}
		if PendingPatch(dir, e.Month) {
			problems = append(problems, fmt.Sprintf("month %s: a patch stopped partway; complete or undo it with: backend run-script coldstore-recover --dir %s --month %s",
				e.Month, dir, e.Month))
		}
		if len(p) > 0 {
			problems = append(problems, fmt.Sprintf("month %s: %s; restore it with: %s", e.Month, strings.Join(p, ", "), PullCommand(dir, "--month "+e.Month)))
		}
	}
	if len(problems) > 0 {
		return &LocalFilesError{Dir: dir, Problems: problems}
	}
	return nil
}

// symlinkProblem is the problem of a file of the cold storage directory that is itself a symbolic link, which
// CheckLocalFiles refuses to serve and BuildManifest refuses to index.
func symlinkProblem(path string) string {
	return path + " is a symbolic link; only the directory and its subdirectories may be links"
}

// PullCommand returns the command that restores files of the cold storage directory from the bucket, for messages.
func PullCommand(dir, what string) string {
	return "backend run-script coldstore-pull --dir " + dir + " " + what + " (see docs/coldstore.md)"
}

// TimeRange is the half-open range [Start, End).
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// Empty tells whether the range holds no instant.
func (r TimeRange) Empty() bool {
	return !r.Start.Before(r.End)
}

// SplitAt splits [start, end) at boundary into the part before it, [start, boundary), and the part from it on,
// [boundary, end). Either part may be empty. Counts and sums over the two parts add up to those over the whole.
func SplitAt(start, end, boundary time.Time) (cold, hot TimeRange) {
	cold = TimeRange{Start: start, End: end}
	if boundary.Before(end) {
		cold.End = boundary
	}
	hot = TimeRange{Start: start, End: end}
	if boundary.After(start) {
		hot.Start = boundary
	}
	return cold, hot
}

// View is the cold storage as one version of the manifest describes it. It never changes.
type View struct {
	dir      string
	manifest *Manifest
	boundary time.Time
}

func newView(dir string, m *Manifest) *View {
	return &View{dir: dir, manifest: m, boundary: HotBoundary(m)}
}

// Manifest returns the manifest of the view. It must not be modified.
func (v *View) Manifest() *Manifest {
	return v.manifest
}

// HotBoundary returns the first instant that is not archived: see HotBoundary.
func (v *View) HotBoundary() time.Time {
	return v.boundary
}

// Split splits [start, end) into the part the cold storage holds and the part to read from the hot database.
func (v *View) Split(start, end time.Time) (cold, hot TimeRange) {
	return SplitAt(start, end, v.boundary)
}

// Files returns the absolute paths of the files of a kind of the archived months that overlap [r.Start, r.End),
// in the order of the months, or none if r is empty or outside the archive. A month's file holds all its reports,
// so the query must still filter on created_at.
func (v *View) Files(kind Kind, r TimeRange) []string {
	var files []string
	for _, mf := range v.MonthFiles(kind, r) {
		files = append(files, mf.Path)
	}
	return files
}

// MonthFile is the file of a kind of an archived month.
type MonthFile struct {
	// Month is the month's manifest entry, e.g. for its RecruitStageID. It must not be modified.
	Month *MonthEntry
	// Path is the absolute path of the file.
	Path string
}

// MonthFiles is Files with the manifest entry of each file's month.
func (v *View) MonthFiles(kind Kind, r TimeRange) []MonthFile {
	if r.Empty() {
		return nil
	}
	var files []MonthFile
	for i := range v.manifest.Months {
		e := &v.manifest.Months[i]
		month, err := ParseMonth(e.Month)
		if err != nil || !month.End.After(r.Start) || (!month.IsPre() && !month.Start.Before(r.End)) {
			continue
		}
		if fe := e.File(kind); fe != nil {
			files = append(files, MonthFile{Month: e, Path: v.path(fe.Path)})
		}
	}
	return files
}

// DimFile returns the absolute path of a dimension file (DropPatternElementsFile).
func (v *View) DimFile(path string) string {
	return v.path(path)
}

func (v *View) path(p string) string {
	return filepath.Join(v.dir, filepath.FromSlash(p))
}

// readParquet returns a read_parquet call over an explicit list of files (see QueryFiles.ReadParquet).
func readParquet(files []string) string {
	if len(files) == 0 {
		panic("coldstore: read_parquet of no file")
	}
	quoted := make([]string, len(files))
	for i, f := range files {
		quoted[i] = quote(f)
	}
	return "read_parquet([" + strings.Join(quoted, ", ") + "])"
}

func unixSeconds(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.Unix())
}
