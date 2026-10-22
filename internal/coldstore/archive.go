package coldstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun/driver/pgdriver"

	"exusiai.dev/backend-next/internal/pkg/observability"
)

// The monthly archiving moves the months whose partitions are old enough from PostgreSQL to the cold storage
// directory the app serves from (on the local disk of the machine that runs the app and the job) and to the backup
// bucket, then drops their partitions. For each month, oldest first, it:
//
//  0. checks that the local manifest holds pre and every month from Epoch on without a gap (what the app serves),
//     and either the month already or the month just before it, so that the month never lands after a gap (or in
//     an empty or wrong directory, whose manifest would then replace the bucket's);
//  1. checks that the directory and the work directory keep at least MinFreeBytes free after the month's files and
//     the month's DuckDB database (estimated from the size of the partitions), and refuses the month otherwise;
//  2. exports the drop pattern elements again (append-only, so a superset) and verifies them against PostgreSQL;
//  3. exports the month into the directory (files written to temporary names, checked against the rows read, then
//     renamed into place), unless the local manifest already has it (a run that stopped after step 6), in which case
//     the files are checked against the manifest;
//  4. uploads the month's files, the drop pattern elements and every other file of the local manifest that the
//     bucket does not hold as the manifest describes it (e.g. a month patched since its upload) with their SHA-256,
//     which S3 checks, and confirms each object's SHA-256 with a HEAD (an object that is already there with the
//     same SHA-256 is kept), so that the bucket's manifest never lists an object the bucket does not hold;
//  5. in a transaction that holds the month's partitions in SHARE mode, so that no row of them can change until they
//     are dropped, reads the month again from PostgreSQL and compares the files with it (full-column content
//     hashes), and checks that the drop pattern elements of step 2 hold the patterns of the month's reports (a late
//     report can bring a new pattern after step 2: the month is then tried once more, from step 0);
//  6. uploads the new manifest.json with the month, after keeping the bucket's previous one under
//     ManifestVersionsDir, then writes the local manifest atomically; then, the month being in the local manifest,
//     records the time (MonthEntry.RecordedAt) in both manifests the same way (see recordMonth);
//  7. once the month has been in the local manifest for DropDelay, and if a reader can load that manifest (every
//     file of it in the directory, see checkServable, which step 6 checks too before publishing), detaches and drops
//     the month's partitions in the same transaction, and commits. A lock conflict in steps 5 to 7 rolls the transaction back, which releases its
//     locks, and they are tried again, up to LockTries times.
//
// The drop waits for DropDelay because a reader (coldstore.Reader) serves the months of the manifest it last
// loaded, and PostgreSQL from the end of their last month on (the hot boundary): until it loads the manifest with
// the month, it reads the month from PostgreSQL. So the run that publishes a month or records its time keeps its
// partitions (the transaction rolls back), and a later run, at least DropDelay after the recorded time, goes
// through the steps again (the files are checked instead of exported, and nothing is uploaded again) and drops
// them.
//
// A failure anywhere rolls the transaction back: no partition is dropped unless every step passed. Every step can
// run again after a crash: the export of a month still in PostgreSQL writes the same bytes again, an object already
// in the bucket is not uploaded again, and a month already in the local manifest is checked instead of exported.
//
// It also creates the partitions of the current month and of the next PartitionRunway months (CreatePartitions).

// ArchiveOptions configures the monthly archiving.
type ArchiveOptions struct {
	// Options are those of the DuckDB jobs: PostgresDSN must be the database the partitions are dropped from.
	Options
	// Dir is the cold storage directory the app serves from.
	Dir string
	// After is how long after its end a month is archived (60 days: the hot database keeps 60 to 90 days).
	After time.Duration
	// MinFreeBytes is the free space that must be left on the file systems of Dir and of the work directory once a
	// month is exported.
	MinFreeBytes int64
	// Backup says where the files go in the bucket.
	Backup BackupOptions
	// DropDelay is how long a month stays in the local manifest before its partitions are dropped (default
	// DefaultDropDelay). It must be longer than the time every reader of the directory takes to load a new manifest
	// (coldstore.Reader checks it every minute by default).
	DropDelay time.Duration
	// LockTimeout bounds each wait for the locks of the partitions (and is the pause between two tries), LockTries
	// the tries of the lock, the verification and the drop (default 10s, 6).
	LockTimeout time.Duration
	LockTries   int
	// Now returns the current time (default time.Now).
	Now func() time.Time
}

// DefaultDropDelay is the default of ArchiveOptions.DropDelay.
const DefaultDropDelay = 15 * time.Minute

// Archiver runs the monthly archiving.
type Archiver struct {
	opts   ArchiveOptions
	db     *sql.DB
	bucket Bucket
}

// NewArchiver checks the options and the cold storage directory: it must exist, be writable, and hold a manifest
// with pre and every month from Epoch on without a gap (see checkArchiveCoverage). db must be the database of
// opts.PostgresDSN.
func NewArchiver(opts ArchiveOptions, db *sql.DB, bucket Bucket) (*Archiver, error) {
	if opts.Dir == "" {
		return nil, errors.New("coldstore: no cold storage directory")
	}
	if opts.PostgresDSN == "" || db == nil || bucket == nil {
		return nil, errors.New("coldstore: the archiving needs PostgreSQL and the backup bucket")
	}
	if opts.After < 31*24*time.Hour {
		return nil, fmt.Errorf("coldstore: archiving months %v after their end would archive months that still gain reports", opts.After)
	}
	if opts.Backup.Prefix == "" {
		opts.Backup.Prefix = Prefix
	}
	if opts.DropDelay <= 0 {
		opts.DropDelay = DefaultDropDelay
	}
	if opts.LockTimeout <= 0 {
		opts.LockTimeout = 10 * time.Second
	}
	if opts.LockTries <= 0 {
		opts.LockTries = 6
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	st, err := os.Stat(opts.Dir)
	if err != nil {
		return nil, errors.Wrap(err, "coldstore: cold storage directory")
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("coldstore: %s is not a directory", opts.Dir)
	}
	probe, err := os.CreateTemp(opts.Dir, ".write-probe-*")
	if err != nil {
		return nil, errors.Wrap(err, "coldstore: cold storage directory is not writable")
	}
	probe.Close()
	_ = os.Remove(probe.Name())
	a := &Archiver{opts: opts, db: db, bucket: bucket}
	mf, err := a.readManifest()
	if err != nil {
		return nil, err
	}
	if err := checkArchiveCoverage(mf); err != nil {
		return nil, err
	}
	return a, nil
}

// ArchiveResult is what a run did.
type ArchiveResult struct {
	// PartitionsCreated tells whether the database has create_report_partitions (and it was called).
	PartitionsCreated bool
	// Archived lists the months archived and dropped from PostgreSQL.
	Archived []string
	// Pending lists the months in the manifest whose partitions are kept until they have been there for DropDelay.
	Pending []string
	// Hot lists the months whose partitions are kept.
	Hot []string
}

// InsufficientSpaceError is returned when a month is refused for lack of free space.
type InsufficientSpaceError struct {
	Month string
	Dir   string
	Free  int64
	Need  int64
	Min   int64
}

func (e *InsufficientSpaceError) Error() string {
	return fmt.Sprintf("coldstore: not archiving %s: %s has %d bytes free, the month needs about %d, which would leave less than %d",
		e.Month, e.Dir, e.Free, e.Need, e.Min)
}

// testHookArchiveStep, if set, is called after each step of ArchiveMonth; an error from it fails the step.
var testHookArchiveStep func(month, step string) error

func archiveStep(month, step string) error {
	if testHookArchiveStep == nil {
		return nil
	}
	return testHookArchiveStep(month, step)
}

// Run creates the partitions ahead of time, then archives every month whose partitions are over by After, oldest
// first. It stops at the first month that fails.
func (a *Archiver) Run(ctx context.Context) (*ArchiveResult, error) {
	now := a.opts.Now()
	res := &ArchiveResult{}
	var err error
	if res.PartitionsCreated, err = CreatePartitions(ctx, a.db, now, a.opts.LockTimeout); err != nil {
		return res, err
	}
	months, parts, others, err := MonthPartitions(ctx, a.db)
	if err != nil {
		return res, err
	}
	if len(others) > 0 {
		log.Warn().Strs("partitions", others).Msg("coldstore: partitions that are not a month are never archived")
	}
	for _, key := range months {
		m := parts[key][0].Month
		if !m.Over(now, a.opts.After) {
			res.Hot = append(res.Hot, key)
			continue
		}
		dropped, err := a.ArchiveMonth(ctx, m, parts[key])
		if err != nil {
			return res, errors.Wrapf(err, "coldstore: archive %s", key)
		}
		if dropped {
			res.Archived = append(res.Archived, key)
		} else {
			res.Pending = append(res.Pending, key)
		}
	}
	return res, nil
}

// ArchiveMonth archives one month, and drops its partitions if it has been in the local manifest for DropDelay (see
// above). dropped tells whether it did. A month whose drop pattern elements turn out stale under its lock
// (errStaleDims) is tried once more: the try exports them again first.
func (a *Archiver) ArchiveMonth(ctx context.Context, m Month, parts []Partition) (dropped bool, err error) {
	dropped, err = a.archiveMonth(ctx, m, parts)
	if errors.Is(err, errStaleDims) {
		log.Warn().Err(err).Str("month", m.Key).Msg("coldstore: a pattern came in after the drop pattern elements were exported, trying the month again")
		dropped, err = a.archiveMonth(ctx, m, parts)
	}
	return dropped, err
}

func (a *Archiver) archiveMonth(ctx context.Context, m Month, parts []Partition) (dropped bool, err error) {
	started := time.Now()
	dir := a.opts.Dir
	mf, err := a.readManifest()
	if err != nil {
		return false, err
	}
	if err := checkArchiveOrder(mf, m); err != nil {
		return false, err
	}
	if e := mf.Month(m.Key); e != nil && !e.RecordedAt.IsZero() && a.opts.Now().Before(e.RecordedAt.Add(a.opts.DropDelay)) {
		log.Info().Str("month", m.Key).Time("recordedAt", e.RecordedAt).Msg("coldstore: month recorded recently, its partitions are dropped later")
		return false, nil
	}

	// the drop pattern elements before the month's lock: Publish locks DimsTarget, and LockTargets takes the dims
	// first, then the months
	dims, err := a.refreshDims(ctx)
	if err != nil {
		return false, err
	}
	target := MonthTarget(m.Key)
	unlock, err := lockTarget(dir, target)
	if err != nil {
		return false, err
	}
	defer unlock()
	// a patch of the month that stopped partway is completed or undone first
	if rec, err := recoverTarget(dir, target); err != nil {
		return false, err
	} else if rec != RecoveryNone {
		log.Warn().Str("month", m.Key).Str("recovery", string(rec)).Msg("coldstore: recovered a patch that had stopped partway")
	}

	if mf, err = a.readManifest(); err != nil {
		return false, err
	}
	if err := checkArchiveOrder(mf, m); err != nil {
		return false, err
	}
	entry := mf.Month(m.Key)
	if err := a.checkFreeSpace(ctx, m, parts, entry == nil); err != nil {
		return false, err
	}

	if entry == nil {
		if entry, err = ExportMonth(ctx, a.opts.Options, m, dir); err != nil {
			return false, err
		}
	} else {
		log.Info().Str("month", m.Key).Msg("coldstore: month already in the manifest, checking its files")
		if err := VerifyMonth(ctx, a.opts.Options, dir, mf, m, false); err != nil {
			return false, err
		}
	}
	if err := archiveStep(m.Key, "exported"); err != nil {
		return false, err
	}

	// every file the new manifest lists, so that the bucket's manifest never lists an object the bucket lacks
	uploaded := map[string]string{}
	for _, fe := range append(append(append([]FileEntry{}, entry.Files...), *dims), manifestFiles(mf)...) {
		if _, ok := uploaded[fe.Path]; ok {
			continue // the month's and the dims' own entries come first
		}
		if err := uploadFile(ctx, a.bucket, a.opts.Backup, dir, fe); err != nil {
			return false, err
		}
		uploaded[fe.Path] = fe.SHA256
	}
	if err := archiveStep(m.Key, "uploaded"); err != nil {
		return false, err
	}

	conn, err := a.db.Conn(ctx)
	if err != nil {
		return false, errors.Wrap(err, "coldstore: connect to PostgreSQL")
	}
	defer func() {
		// the session's settings must not reach the other users of db
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		conn.Close()
	}()
	// steps 5 to 7 are tried again as a whole after a lock conflict: the rollback of the try releases every lock it
	// took, so that a writer waiting behind them (e.g. an UPDATE through drop_reports by report_id alone, which holds
	// the lock of the partitioned table that DETACH waits for) can finish, and the next try locks and verifies the
	// month again
	rec := &monthRecording{entry: entry, dims: dims, uploaded: uploaded, fresh: mf.Month(m.Key) == nil}
	for try := 1; ; try++ {
		if dropped, err = a.lockVerifyDrop(ctx, conn, m, parts, rec); err == nil || !isLockConflict(err) || try >= a.opts.LockTries {
			break
		}
		log.Warn().Err(err).Str("month", m.Key).Int("try", try).Msg("coldstore: lock conflict, released the locks of the month and trying again")
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(a.opts.LockTimeout):
		}
	}
	if err != nil {
		return false, err
	}
	if !dropped {
		// the readers may still serve a manifest without the month
		log.Info().Str("month", m.Key).Int64("rows", entry.Rows).Time("recordedAt", rec.recordedAt).Dur("took", time.Since(started)).
			Msg("coldstore: recorded month in the manifest, its partitions are dropped by a later run")
		return false, nil
	}
	observability.ColdstoreArchivedMonths.Inc()
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = p.QualifiedName()
	}
	log.Info().Str("month", m.Key).Int64("rows", entry.Rows).Strs("partitions", names).Dur("took", time.Since(started)).
		Msg("coldstore: archived month and dropped its partitions")
	return true, nil
}

// monthRecording is what steps 5 to 7 of ArchiveMonth work with, and what they did, across their tries.
type monthRecording struct {
	entry    *MonthEntry
	dims     *FileEntry
	uploaded map[string]string
	// fresh tells whether the month was exported by this run (it is not in the local manifest yet)
	fresh bool
	// started tells whether this run published the month in the local manifest or started its DropDelay: it then
	// keeps the partitions
	started    bool
	recordedAt time.Time
}

// lockVerifyDrop is one try of steps 5 to 7 of ArchiveMonth, in a transaction of its own on conn, which a failure
// rolls back (releasing every lock it took). dropped tells whether it dropped the partitions and committed.
func (a *Archiver) lockVerifyDrop(ctx context.Context, conn *sql.Conn, m Month, parts []Partition, rec *monthRecording) (dropped bool, err error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.Wrap(err, "coldstore: begin")
	}
	defer tx.Rollback() //nolint:errcheck
	for _, s := range []string{
		// the transaction waits, idle, for the verification below
		"SET LOCAL idle_in_transaction_session_timeout = 0",
		fmt.Sprintf("SET LOCAL lock_timeout = %d", a.opts.LockTimeout.Milliseconds()),
		// SHARE: the month can still be read (the verification reads it), but no row of it can change until it is
		// dropped. Writes to other months take locks only on their partitions (a recall too, see
		// repo.RecallMaxAge); an UPDATE by report_id alone waits until the transaction ends. LOCK TABLE takes the
		// locks one by one, in the order of the report worker's writes (see lockOrder).
		"LOCK TABLE " + strings.Join(lockOrder(parts), ", ") + " IN SHARE MODE",
	} {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return false, errors.Wrapf(err, "coldstore: %s", s)
		}
	}
	if err := VerifyMonth(ctx, a.opts.Options, a.opts.Dir, &Manifest{Months: []MonthEntry{*rec.entry}}, m, true); err != nil {
		return false, err
	}
	if err := archiveStep(m.Key, "verified"); err != nil {
		return false, err
	}
	// the drop pattern elements were exported before the month's lock: a late report of the month with a new
	// pattern can have come in between (see errStaleDims)
	if err := verifyDimsCoverMonth(ctx, a.opts.Options, a.opts.Dir, m); err != nil {
		return false, err
	}
	if err := a.recordMonth(ctx, m, rec); err != nil {
		return false, err
	}
	if err := archiveStep(m.Key, "recorded"); err != nil {
		return false, err
	}
	// the transaction rolls back: the readers may still serve a manifest without the month
	if rec.started || a.opts.Now().Before(rec.recordedAt.Add(a.opts.DropDelay)) {
		return false, nil
	}

	// a reader serves the month only once it loads a manifest that passes its checks: until then it reads the
	// month from PostgreSQL, so the partitions stay while the local directory could not be served (a file of another
	// month missing since its upload, which the bucket still holds, a patch stopped partway)
	if err := a.checkServable(); err != nil {
		return false, err
	}
	if err := dropPartitions(ctx, tx, parts); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, errors.Wrap(err, "coldstore: commit the drop of the partitions")
	}
	return true, nil
}

// recordMonth is step 6 of ArchiveMonth. A month exported by this run is first published in both manifests (the
// bucket's, then the local one) without RecordedAt. Then, once the month is in the local manifest (the readers can
// load it from then on), its RecordedAt is set to the current time, in both manifests too. The DropDelay of the
// month thus starts after its local publication, however long the uploads take, and a run that stopped between the
// two (or whose second upload failed) leaves the month without RecordedAt, so that the next run starts the DropDelay
// then. The run that publishes the month or sets its RecordedAt keeps the partitions (rec.started).
//
// The manifest is written only for that; the bucket's is replaced whenever it is not the local one (e.g. after the
// drop pattern elements grew while the month waited for DropDelay).
func (a *Archiver) recordMonth(ctx context.Context, m Month, rec *monthRecording) error {
	name := filepath.Join(a.opts.Dir, ManifestFile)
	check := func(mf *Manifest) (*MonthEntry, error) {
		cur := mf.Month(m.Key)
		if cur != nil && !sameFiles(cur, rec.entry) {
			return nil, fmt.Errorf("coldstore: the manifest's entry of %s changed during the archiving", m.Key)
		}
		if cur := mf.Dim(DropPatternElementsFile); cur == nil || cur.SHA256 != rec.dims.SHA256 {
			return nil, fmt.Errorf("coldstore: %s changed during the archiving", DropPatternElementsFile)
		}
		return cur, nil
	}
	commit := func(mf *Manifest, content []byte) error {
		// a file changed since step 4 (e.g. another month patched meanwhile) must be in the bucket too
		for _, fe := range manifestFiles(mf) {
			if rec.uploaded[fe.Path] != fe.SHA256 {
				if err := confirmObject(ctx, a.bucket, a.opts.Backup.key(fe.Path), fe.Bytes, fe.SHA256); err != nil {
					return errors.Wrapf(err, "coldstore: %s changed during the archiving and is not in the bucket", fe.Path)
				}
			}
		}
		return uploadManifest(ctx, a.bucket, a.opts.Backup, content)
	}

	if rec.fresh {
		err := updateManifest(name, func(mf *Manifest) error {
			cur, err := check(mf)
			if err != nil {
				return err
			}
			if cur != nil {
				return fmt.Errorf("coldstore: %s was recorded in the manifest during the archiving", m.Key)
			}
			published := *rec.entry
			published.RecordedAt = time.Time{}
			mf.PutMonth(published)
			if err := checkArchiveCoverage(mf); err != nil {
				return err
			}
			// a manifest the readers would refuse is not published (see checkServable)
			return CheckLocalFiles(a.opts.Dir, mf)
		}, commit)
		if err != nil {
			return err
		}
		rec.fresh, rec.started = false, true
		if err := archiveStep(m.Key, "published"); err != nil {
			return err
		}
	}

	return updateManifest(name, func(mf *Manifest) error {
		cur, err := check(mf)
		if err != nil {
			return err
		}
		if cur == nil {
			return fmt.Errorf("coldstore: %s is not in the local manifest", m.Key)
		}
		if !cur.RecordedAt.IsZero() {
			rec.recordedAt = cur.RecordedAt
			return errManifestUnchanged
		}
		// the month is in the local manifest: the readers can load it from now on
		cur.RecordedAt = a.opts.Now().UTC()
		rec.recordedAt, rec.started = cur.RecordedAt, true
		return nil
	}, commit)
}

// readManifest reads the local manifest, which must exist: the archiving only adds months to a cold storage
// directory that holds the earlier ones.
func (a *Archiver) readManifest() (*Manifest, error) {
	mf, err := ReadManifest(filepath.Join(a.opts.Dir, ManifestFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("coldstore: %s has no %s: the archiving only adds months to a cold storage directory that holds "+
			"the earlier ones (restore it with run-script coldstore-pull, or check COLDSTORE_DIRECTORY)", a.opts.Dir, ManifestFile)
	}
	return mf, err
}

// checkServable checks that a reader (coldstore.Reader) of the cold storage directory can load its local manifest:
// the same checks as Reader.Reload (CheckCoverage, then CheckLocalFiles, which reads no file but checks that each
// one is there with its size). The bucket holding a file is not enough: the app never reads the bucket.
func (a *Archiver) checkServable() error {
	mf, err := a.readManifest()
	if err != nil {
		return err
	}
	if err := CheckCoverage(mf); err != nil {
		return errors.Wrap(err, "coldstore: the readers cannot serve the local manifest, keeping the partitions")
	}
	if err := CheckLocalFiles(a.opts.Dir, mf); err != nil {
		return errors.Wrap(err, "coldstore: the readers cannot serve the local manifest, keeping the partitions")
	}
	return nil
}

// checkArchiveCoverage checks that mf holds what the app serves: pre and every month from Epoch to its last month,
// without a gap, each with all its files, and the drop pattern elements. The archiving never adds a month to (nor
// uploads the manifest of) a directory that does not.
func checkArchiveCoverage(mf *Manifest) error {
	var problems []string
	if len(mf.Months) == 0 {
		problems = append(problems, "it has no month")
	}
	want, next := PreMonth, Epoch
	for _, e := range mf.Months {
		if e.Month != want {
			problems = append(problems, fmt.Sprintf("month %s is missing (the next month in it is %s)", want, e.Month))
			break
		}
		for _, kind := range Kinds {
			if e.File(kind) == nil {
				problems = append(problems, fmt.Sprintf("month %s has no %s file", e.Month, kind))
			}
		}
		want, next = next.Format("2006-01"), next.AddDate(0, 1, 0)
	}
	if mf.Dim(DropPatternElementsFile) == nil {
		problems = append(problems, DropPatternElementsFile+" is missing")
	}
	if len(problems) > 0 {
		return errors.New("coldstore: the local manifest does not hold every month up to its last one, refusing to archive into it: " +
			strings.Join(problems, "; "))
	}
	return nil
}

// checkArchiveOrder checks that month m can be archived into a directory whose manifest is mf: mf must pass
// checkArchiveCoverage and hold either m already or the month just before it.
func checkArchiveOrder(mf *Manifest, m Month) error {
	if err := checkArchiveCoverage(mf); err != nil {
		return err
	}
	if mf.Month(m.Key) != nil {
		return nil
	}
	prev := PreMonth
	if m.Start.After(Epoch) {
		prev = m.Start.AddDate(0, -1, 0).Format("2006-01")
	}
	if last := mf.Months[len(mf.Months)-1].Month; last != prev {
		return fmt.Errorf("coldstore: not archiving %s: the last month of the local manifest is %s, not %s", m.Key, last, prev)
	}
	return nil
}

// manifestFiles returns every file mf lists.
func manifestFiles(mf *Manifest) []FileEntry {
	files := append([]FileEntry{}, mf.Dims...)
	for _, e := range mf.Months {
		files = append(files, e.Files...)
	}
	return files
}

// refreshDims exports the drop pattern elements again, records them in the local manifest (the file only gains
// rows, so the months already archived still find their patterns in it) and verifies them against PostgreSQL.
func (a *Archiver) refreshDims(ctx context.Context) (*FileEntry, error) {
	var fe *FileEntry
	err := Publish(a.opts.Dir, DimsTarget, func(m *Manifest) (func(*Manifest), error) {
		var err error
		prev := m.Dim(DropPatternElementsFile)
		if fe, err = ExportDims(ctx, a.opts.Options, a.opts.Dir, prev); err != nil {
			return nil, err
		}
		if prev != nil && *prev == *fe {
			return nil, nil // the same bytes: the manifest is left as it is
		}
		return func(m *Manifest) { m.PutDim(*fe) }, nil
	})
	if err != nil {
		return nil, err
	}
	mf, err := ReadManifest(filepath.Join(a.opts.Dir, ManifestFile))
	if err != nil {
		return nil, err
	}
	if err := VerifyDims(ctx, a.opts.Options, a.opts.Dir, mf, true); err != nil {
		return nil, err
	}
	return fe, nil
}

// checkFreeSpace refuses the month unless MinFreeBytes are left on the file systems of the cold storage directory
// and of the work directory after its export: the files take about a tenth of the partitions' size in PostgreSQL
// (indexes included), and the DuckDB database of the export and of the verification about as much as the
// partitions (6 GB for the 48.6M reports of 2026-09). exporting is false when the files are already there.
func (a *Archiver) checkFreeSpace(ctx context.Context, m Month, parts []Partition, exporting bool) error {
	lits := make([]string, len(parts))
	for i, p := range parts {
		lits[i] = "(" + quote(p.QualifiedName()) + ")"
	}
	var size int64
	if err := a.db.QueryRowContext(ctx, `SELECT coalesce(sum(pg_total_relation_size(x::regclass)), 0)::bigint
		FROM (VALUES `+strings.Join(lits, ", ")+`) v(x)`).Scan(&size); err != nil {
		return errors.Wrap(err, "coldstore: size of the partitions")
	}
	work := a.opts.WorkDir
	if work == "" {
		work = os.TempDir()
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return errors.Wrap(err, "coldstore: create work directory")
	}
	storeFree, storeDev, err := freeSpace(a.opts.Dir)
	if err != nil {
		return err
	}
	workFree, workDev, err := freeSpace(work)
	if err != nil {
		return err
	}
	observability.ColdstoreFreeBytes.WithLabelValues("store").Set(float64(storeFree))
	observability.ColdstoreFreeBytes.WithLabelValues("work").Set(float64(workFree))

	storeNeed, workNeed := int64(0), size
	if exporting {
		storeNeed = size / 10
	}
	if storeDev == workDev {
		storeNeed += workNeed
		workNeed = 0
	}
	if storeFree-storeNeed < a.opts.MinFreeBytes {
		return &InsufficientSpaceError{Month: m.Key, Dir: a.opts.Dir, Free: storeFree, Need: storeNeed, Min: a.opts.MinFreeBytes}
	}
	if workNeed > 0 && workFree-workNeed < a.opts.MinFreeBytes {
		return &InsufficientSpaceError{Month: m.Key, Dir: work, Free: workFree, Need: workNeed, Min: a.opts.MinFreeBytes}
	}
	return nil
}

// freeSpace returns the bytes available to the process on the file system of dir, and its device.
func freeSpace(dir string) (int64, uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0, errors.Wrapf(err, "coldstore: statfs %s", dir)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return 0, 0, errors.Wrap(err, "coldstore: stat")
	}
	var dev uint64
	if s, ok := fi.Sys().(*syscall.Stat_t); ok {
		dev = uint64(s.Dev) //nolint:unconvert // Dev is not a uint64 on every platform
	}
	return int64(st.Bavail) * int64(st.Bsize), dev, nil //nolint:unconvert
}

// isLockConflict tells whether err is PostgreSQL's lock_not_available (a lock_timeout) or deadlock_detected, or
// errLockWaiters.
func isLockConflict(err error) bool {
	var pgErr pgdriver.Error
	return errors.Is(err, errLockWaiters) || errors.As(err, &pgErr) && (pgErr.Field('C') == "55P03" || pgErr.Field('C') == "40P01")
}

// ArchiveOutcome is the outcome label of observability.ColdstoreArchiveRuns for the error of a run.
func ArchiveOutcome(err error) string {
	var space *InsufficientSpaceError
	switch {
	case err == nil:
		return "ok"
	case errors.As(err, &space):
		return "insufficient_space"
	case IsVerificationError(err):
		return "verification_failed"
	default:
		return "error"
	}
}
