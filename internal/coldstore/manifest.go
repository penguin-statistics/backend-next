package coldstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"

	"exusiai.dev/backend-next/internal/pkg/bininfo"
)

// SchemaVersion is the version of the manifest's format.
const SchemaVersion = 1

// Manifest indexes a cold storage directory. Paths are relative to the directory the files are read from, which
// need not be the manifest's: a manifest of files elsewhere (e.g. read-only ones) can be written anywhere, and a
// directory of symbolic links to their subdirectories (drop_reports, recruit, ...) next to it makes a complete
// cold storage directory.
type Manifest struct {
	SchemaVersion int       `json:"schema_version"`
	Layout        string    `json:"layout"`
	GeneratedAt   time.Time `json:"generated_at"`
	Generator     string    `json:"generator"`
	// Dims are the dimension files (DropPatternElementsFile).
	Dims []FileEntry `json:"dims"`
	// Months are sorted by key; "pre" sorts first.
	Months []MonthEntry `json:"months"`
}

// MonthEntry describes one archived month.
type MonthEntry struct {
	Month string `json:"month"`
	MonthStats
	// RecruitStageID is the stage_id of the recruit reports, which the recruit file leaves out.
	RecruitStageID int `json:"recruit_stage_id"`
	// Files are the month's files, in the order of Kinds.
	Files       []FileEntry `json:"files"`
	GeneratedAt time.Time   `json:"generated_at"`
	Generator   string      `json:"generator"`
	// RecordedAt is when the monthly archiving found the month in the local manifest (so readers can load it from
	// then on) while its partitions were still in PostgreSQL: they are dropped once readers have had the time to load
	// the manifest (see ArchiveOptions.DropDelay). It is left out for the months recorded otherwise and, until it is
	// set, for a month the archiving publishes; a new entry of the month (e.g. a patch) drops it.
	RecordedAt time.Time `json:"recorded_at,omitzero"`
}

// FileEntry describes one file.
type FileEntry struct {
	// Path is relative to the cold storage directory, with forward slashes.
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Rows   int64  `json:"rows"`
}

// Generator names the program that writes the files and manifests.
func Generator() string {
	return "backend-next " + bininfo.Version
}

// NewManifest returns an empty manifest.
func NewManifest() *Manifest {
	return &Manifest{SchemaVersion: SchemaVersion, Layout: Prefix, Dims: []FileEntry{}, Months: []MonthEntry{}}
}

// Month returns the entry of a month, or nil.
func (m *Manifest) Month(key string) *MonthEntry {
	for i := range m.Months {
		if m.Months[i].Month == key {
			return &m.Months[i]
		}
	}
	return nil
}

// File returns the entry of a file of the month, or nil.
func (e *MonthEntry) File(kind Kind) *FileEntry {
	p := FilePath(kind, e.Month)
	for i := range e.Files {
		if e.Files[i].Path == p {
			return &e.Files[i]
		}
	}
	return nil
}

// Dim returns the entry of a dimension file, or nil.
func (m *Manifest) Dim(path string) *FileEntry {
	for i := range m.Dims {
		if m.Dims[i].Path == path {
			return &m.Dims[i]
		}
	}
	return nil
}

// PutMonth adds or replaces the entry of a month.
func (m *Manifest) PutMonth(e MonthEntry) {
	if old := m.Month(e.Month); old != nil {
		*old = e
		return
	}
	m.Months = append(m.Months, e)
	sort.Slice(m.Months, func(i, k int) bool { return monthLess(m.Months[i].Month, m.Months[k].Month) })
}

// PutDim adds or replaces the entry of a dimension file.
func (m *Manifest) PutDim(f FileEntry) {
	if old := m.Dim(f.Path); old != nil {
		*old = f
		return
	}
	m.Dims = append(m.Dims, f)
	sort.Slice(m.Dims, func(i, k int) bool { return m.Dims[i].Path < m.Dims[k].Path })
}

func monthLess(a, b string) bool {
	if a == PreMonth || b == PreMonth {
		return a == PreMonth && b != PreMonth
	}
	return a < b
}

// ReadManifest reads a manifest file.
func ReadManifest(name string) (*Manifest, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return parseManifest(b, name)
}

func parseManifest(b []byte, name string) (*Manifest, error) {
	m := NewManifest()
	if err := json.Unmarshal(b, m); err != nil {
		return nil, errors.Wrapf(err, "coldstore: parse %s", name)
	}
	if m.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("coldstore: %s has schema version %d, want %d", name, m.SchemaVersion, SchemaVersion)
	}
	return m, nil
}

// WriteManifest writes a manifest file atomically: to a temporary file next to it, flushed, then renamed.
func WriteManifest(name string, m *Manifest) error {
	b, err := MarshalManifest(m)
	if err != nil {
		return err
	}
	return writeManifestFile(name, b)
}

// MarshalManifest returns the content of a manifest file.
func MarshalManifest(m *Manifest) ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// writeManifestFile writes the content of a manifest file atomically.
func writeManifestFile(name string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return errors.Wrap(err, "coldstore: create directory")
	}
	f, err := os.CreateTemp(filepath.Dir(name), filepath.Base(name)+".*.tmp")
	if err != nil {
		return errors.Wrap(err, "coldstore: create manifest")
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	if err == nil {
		err = f.Chmod(0o644)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return errors.Wrap(err, "coldstore: write manifest")
	}
	if err := os.Rename(f.Name(), name); err != nil {
		return errors.Wrap(err, "coldstore: rename manifest")
	}
	d, err := os.Open(filepath.Dir(name))
	if err != nil {
		return errors.Wrap(err, "coldstore: open directory")
	}
	defer d.Close()
	return errors.Wrap(d.Sync(), "coldstore: sync directory")
}

// UpdateManifest applies fn to the manifest file (an empty manifest if there is none) and writes it back. The
// read-modify-write holds an exclusive lock on name+".lock", so concurrent exports into one directory never lose
// each other's entries.
func UpdateManifest(name string, fn func(*Manifest) error) error {
	return updateManifest(name, fn, nil)
}

// errManifestUnchanged is returned by the fn of updateManifest that leaves the manifest as it is.
var errManifestUnchanged = errors.New("coldstore: manifest unchanged")

// updateManifest is UpdateManifest; commit, if not nil, is called with the new manifest and its content once fn has
// applied its changes, before the file is written, still holding the lock: an error from it leaves the file as it
// was. The monthly archiving uploads the content to the bucket there, so that the bucket's manifest is the local one.
// If fn returns errManifestUnchanged, the file is not written, and commit gets its current content.
func updateManifest(name string, fn func(*Manifest) error, commit func(m *Manifest, content []byte) error) error {
	unlock, err := lockManifest(name)
	if err != nil {
		return err
	}
	defer unlock()

	m, err := ReadManifest(name)
	if errors.Is(err, os.ErrNotExist) {
		m, err = NewManifest(), nil
	}
	if err != nil {
		return err
	}
	if err := fn(m); errors.Is(err, errManifestUnchanged) {
		if commit == nil {
			return nil
		}
		b, err := os.ReadFile(name)
		if err != nil {
			return errors.Wrap(err, "coldstore: read manifest")
		}
		return commit(m, b)
	} else if err != nil {
		return err
	}
	m.GeneratedAt = time.Now().UTC()
	m.Generator = Generator()
	b, err := MarshalManifest(m)
	if err != nil {
		return err
	}
	if commit != nil {
		if err := commit(m, b); err != nil {
			return err
		}
	}
	return writeManifestFile(name, b)
}

// lockManifest takes the exclusive lock of UpdateManifest on the manifest name (name+".lock"), and returns its
// release.
func lockManifest(name string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return nil, errors.Wrap(err, "coldstore: create directory")
	}
	lock, err := os.OpenFile(name+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, errors.Wrap(err, "coldstore: open manifest lock")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		lock.Close()
		return nil, errors.Wrap(err, "coldstore: lock manifest")
	}
	return func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		lock.Close()
	}, nil
}

// BuildManifest indexes the files of a cold storage directory without writing to it: every month with a
// drop_reports file (all four files are required) and the dimension files present. The statistics are computed
// from the files, so a verification against PostgreSQL (Verify) is what ties them to the source. Like OpenReader
// (CheckLocalFiles), it refuses a file that is itself a symbolic link; the directory and its subdirectories may be
// links (see Manifest).
func BuildManifest(ctx context.Context, opts Options, dir string) (*Manifest, error) {
	months, err := listMonths(dir)
	if err != nil {
		return nil, err
	}
	return buildManifest(ctx, opts, dir, months)
}

// listMonths lists the months of dir that have a drop_reports file, in order.
func listMonths(dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, string(KindDropReports)))
	if err != nil {
		return nil, errors.Wrap(err, "coldstore: list months")
	}
	var months []string
	for _, e := range entries {
		if key, ok := monthOfFile(KindDropReports, e.Name()); ok {
			months = append(months, key)
		}
	}
	sort.Slice(months, func(i, k int) bool { return monthLess(months[i], months[k]) })
	return months, nil
}

// testHookRebuildIndexed, if set, is called by RebuildManifest once it has indexed the files, before it writes the
// manifest.
var testHookRebuildIndexed func()

// RebuildManifest builds the manifest of dir from its files (see BuildManifest) and writes it as dir's own manifest.
// An existing manifest is replaced only with force. The rebuild holds the locks of Publish on the dims and on every
// month that has files or an entry in dir, from before the files are indexed until the manifest is written, so that
// no export or patch of those changes the files or the manifest meanwhile: the manifest it writes describes the
// files as they are. It fails, rather than drop the entry, if an export records another month meanwhile.
//
// Whether the manifest is unreadable is decided again under the manifest's lock, when it is written: another rebuild
// may have repaired it meanwhile, and the repaired manifest then gets the checks of a readable one. A manifest that
// is still unreadable is replaced only if no month got files meanwhile, since its entries cannot be checked.
func RebuildManifest(ctx context.Context, opts Options, dir string, force bool) (*Manifest, error) {
	name := filepath.Join(dir, ManifestFile)
	cur, err := ReadManifest(name)
	// a manifest that cannot be read (e.g. corrupted) is replaced only with force
	switch {
	case errors.Is(err, os.ErrNotExist):
		cur = nil
	case err != nil && !force:
		return nil, err
	case err != nil:
		cur = nil
	case !force:
		return nil, fmt.Errorf("coldstore: %s exists; pass --force to replace it with a manifest built from the files", name)
	}
	months, err := listMonths(dir)
	if err != nil {
		return nil, err
	}
	locked := map[string]bool{}
	for _, key := range months {
		locked[key] = true
	}
	if cur != nil {
		for _, e := range cur.Months {
			locked[e.Month] = true
		}
	}
	keys := make([]string, 0, len(locked))
	for key := range locked {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, k int) bool { return monthLess(keys[i], keys[k]) })
	targets := []string{DimsTarget}
	for _, key := range keys {
		targets = append(targets, MonthTarget(key))
	}
	unlock, err := LockTargets(dir, targets)
	if err != nil {
		return nil, err
	}
	defer unlock()
	// a month whose patch stopped partway has files of two generations
	pending, err := journalMonths(dir)
	if err != nil {
		return nil, err
	}
	if len(pending) > 0 {
		return nil, fmt.Errorf("coldstore: a patch of %s stopped partway: run coldstore-recover first", strings.Join(pending, ", "))
	}

	m, err := buildManifest(ctx, opts, dir, months)
	if err != nil {
		return nil, err
	}
	if testHookRebuildIndexed != nil {
		testHookRebuildIndexed()
	}
	if err := commitRebuild(name, dir, m, locked, force); err != nil {
		return nil, err
	}
	return m, nil
}

// commitRebuild writes the manifest m built by RebuildManifest as name, holding the manifest's lock while it reads
// the current one and decides how to replace it, so that the decision holds when the manifest is written. locked is
// the set of months the rebuild holds the locks of: a month outside it that has an entry (a readable manifest) or
// files (an unreadable one) was published meanwhile, and replacing the manifest would drop it.
func commitRebuild(name, dir string, m *Manifest, locked map[string]bool, force bool) error {
	unlock, err := lockManifest(name)
	if err != nil {
		return err
	}
	defer unlock()

	cur, err := ReadManifest(name)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		// still (or newly) unreadable: its entries cannot be checked, but those of a month published meanwhile
		// come with the month's files
		if !force {
			return err
		}
		months, err := listMonths(dir)
		if err != nil {
			return err
		}
		for _, key := range months {
			if !locked[key] {
				return fmt.Errorf("coldstore: month %s was exported while the files were indexed; run again", key)
			}
		}
	default:
		if !force && (len(cur.Months) > 0 || len(cur.Dims) > 0) {
			return fmt.Errorf("coldstore: %s was written while the files were indexed; pass --force to replace it", name)
		}
		for _, e := range cur.Months {
			if !locked[e.Month] {
				return fmt.Errorf("coldstore: month %s was recorded in %s while the files were indexed; run again", e.Month, name)
			}
		}
	}
	return WriteManifest(name, m)
}

// buildManifest indexes the given months of dir and the dimension files present.
func buildManifest(ctx context.Context, opts Options, dir string, months []string) (*Manifest, error) {
	j, err := newJob(ctx, opts, "manifest", false)
	if err != nil {
		return nil, err
	}
	defer j.Close()

	m := NewManifest()
	for _, key := range months {
		started := time.Now()
		e, err := j.indexMonth(ctx, dir, key)
		if err != nil {
			return nil, errors.Wrapf(err, "coldstore: index %s", key)
		}
		m.PutMonth(*e)
		log.Info().Str("month", key).Int64("rows", e.Rows).Dur("took", time.Since(started)).Msg("coldstore: indexed month")
	}
	if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(DropPatternElementsFile))); err == nil {
		if err := checkIndexable(dir, DropPatternElementsFile); err != nil {
			return nil, err
		}
		fe, err := describeExisting(dir, DropPatternElementsFile)
		if err != nil {
			return nil, err
		}
		if fe.Rows, err = j.countRows(ctx, filepath.Join(dir, filepath.FromSlash(DropPatternElementsFile))); err != nil {
			return nil, err
		}
		m.PutDim(fe)
	}
	m.GeneratedAt = time.Now().UTC()
	m.Generator = Generator()
	return m, nil
}

func (j *job) indexMonth(ctx context.Context, dir, key string) (*MonthEntry, error) {
	files := monthFiles{}
	for _, kind := range Kinds {
		files[kind] = filepath.Join(dir, filepath.FromSlash(FilePath(kind, key)))
		if err := checkIndexable(dir, FilePath(kind, key)); err != nil {
			return nil, err
		}
		kvMonth, err := j.kvMetadata(ctx, files[kind], "month")
		if err != nil {
			return nil, err
		}
		if kvMonth != key {
			return nil, fmt.Errorf("coldstore: %s has month %q in its metadata", files[kind], kvMonth)
		}
	}
	stageID, err := j.kvMetadata(ctx, files[KindRecruit], "stage_id")
	if err != nil {
		return nil, err
	}
	recruitStageID, err := strconv.Atoi(stageID)
	if err != nil {
		return nil, fmt.Errorf("coldstore: %s has no stage_id in its metadata", files[KindRecruit])
	}

	e := &MonthEntry{Month: key, RecruitStageID: recruitStageID, GeneratedAt: time.Now().UTC(), Generator: Generator()}
	e.Files = make([]FileEntry, len(Kinds))
	// hash the files while DuckDB reads them
	var g errgroup.Group
	for i, kind := range Kinds {
		g.Go(func() error {
			fe, err := describeExisting(dir, FilePath(kind, key))
			e.Files[i] = fe
			return err
		})
	}
	stats, err := j.fileStats(ctx, files, recruitStageID)
	if gerr := g.Wait(); err == nil {
		err = gerr
	}
	if err != nil {
		return nil, err
	}
	e.MonthStats = stats
	for i, kind := range Kinds {
		if e.Files[i].Rows, err = j.countRows(ctx, files[kind]); err != nil {
			return nil, err
		}
	}
	cnt, times, err := j.rollupTotals(ctx, files[KindRollup])
	if err != nil {
		return nil, err
	}
	if cnt != stats.ReliableRows || times != stats.ReliableTimes {
		return nil, fmt.Errorf("coldstore: rollup has cnt %d, times %d; the reports have reliable_rows %d, reliable_times %d",
			cnt, times, stats.ReliableRows, stats.ReliableTimes)
	}
	return e, nil
}

// checkIndexable checks that a file BuildManifest indexes is in dir and, as CheckLocalFiles requires, not itself a
// symbolic link (its directory may be one), so that OpenReader serves every manifest BuildManifest writes.
func checkIndexable(dir, path string) error {
	st, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(path)))
	if err != nil {
		return errors.Wrap(err, "coldstore: missing file")
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return errors.New("coldstore: " + symlinkProblem(path))
	}
	return nil
}

// kvMetadata returns a key-value metadata entry of a Parquet file, or "" if it has none.
func (j *job) kvMetadata(ctx context.Context, file, key string) (string, error) {
	var v *string
	err := j.queryRow(ctx, `SELECT max(decode(value)) FROM parquet_kv_metadata([`+quote(file)+`])
		WHERE decode(key) = `+quote(key), &v)
	if err != nil || v == nil {
		return "", err
	}
	return *v, nil
}

// describeExisting returns the size and SHA-256 of a file of the cold storage directory, without writing to it.
func describeExisting(dir, path string) (FileEntry, error) {
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(path)))
	if err != nil {
		return FileEntry{}, errors.Wrap(err, "coldstore: open")
	}
	defer f.Close()
	fe, err := hashFile(f)
	fe.Path = path
	return fe, err
}
