package coldstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// parquetOptions are the COPY options of every file. V2 data pages use the DELTA encodings (8% smaller than V1 on
// the reports); higher zstd levels and smaller row groups were measured to gain nothing.
const parquetOptions = "FORMAT parquet, COMPRESSION zstd, PARQUET_VERSION V2, ROW_GROUP_SIZE 122880"

// manualSourcesList is the SQL list of the source_names of manually entered reports, the is_manual of the rollup
// (as isManualSourceExpr in internal/repo).
func manualSourcesList() string {
	quoted := make([]string, len(constant.ManualSources))
	for i, s := range constant.ManualSources {
		quoted[i] = quote(s)
	}
	return strings.Join(quoted, ", ")
}

// ExportMonth reads a month's reports and extras from PostgreSQL and writes the month's four files below dir. The
// files are written next to their final paths and renamed into place only after their content was read back and
// matched the source (MonthStats) and the rollup's totals matched the reports, so a failed export leaves the
// previous files, if any, untouched. It returns the month's manifest entry; recording it is up to the caller (see
// UpdateManifest).
func ExportMonth(ctx context.Context, opts Options, month Month, dir string) (*MonthEntry, error) {
	return exportMonth(ctx, opts, month, dir, nil)
}

// ReexportMonth exports a month that is already archived again, e.g. one changed in PostgreSQL after its export,
// as ExportMonth does. It refuses, before any file is replaced, when PostgreSQL holds fewer reports of the month
// than archived: the app never deletes reports, so fewer means that the month is (partly) gone from PostgreSQL, e.g.
// its partition was dropped after the migration, and that the archive is the only copy left. Reports are removed
// from an archived month with PatchMonth instead.
func ReexportMonth(ctx context.Context, opts Options, month Month, dir string, archived *MonthEntry) (*MonthEntry, error) {
	return exportMonth(ctx, opts, month, dir, func(src MonthStats) error {
		if src.Rows < archived.Rows {
			return fmt.Errorf("coldstore: PostgreSQL holds %d reports of %s, the archive %d: not replacing the archive "+
				"with fewer reports (is the month still in PostgreSQL?)", src.Rows, month.Key, archived.Rows)
		}
		return nil
	})
}

// exportMonth is ExportMonth; check, if not nil, sees the month's statistics in PostgreSQL before any file is
// written, and an error from it ends the export.
func exportMonth(ctx context.Context, opts Options, month Month, dir string, check func(MonthStats) error) (*MonthEntry, error) {
	started := time.Now()
	j, err := newJob(ctx, opts, "export-"+month.Key, true)
	if err != nil {
		return nil, err
	}
	defer j.Close()

	recruitStageID, err := j.recruitStageID(ctx)
	if err != nil {
		return nil, err
	}
	if err := j.loadSource(ctx, month); err != nil {
		return nil, err
	}
	log.Info().Str("month", month.Key).Dur("took", time.Since(started)).Msg("coldstore: read month from PostgreSQL")
	entry, err := j.writeMonth(ctx, month, dir, recruitStageID, check)
	if err != nil {
		return nil, err
	}
	log.Info().Str("month", month.Key).Int64("rows", entry.Rows).Int64("recruitRows", entry.RecruitRows).
		Int64("extrasRows", entry.ExtrasRows).Dur("took", time.Since(started)).Msg("coldstore: exported month")
	return entry, nil
}

// writeMonth writes the month's files from the tables r and e (see loadSource) below dir, replacing the previous
// ones (see stageMonth). check, if not nil, sees the statistics of r and e before anything is written; an error from
// it is returned.
func (j *job) writeMonth(ctx context.Context, month Month, dir string, recruitStageID int, check func(MonthStats) error) (*MonthEntry, error) {
	s, err := j.stageMonth(ctx, month, dir, recruitStageID, check)
	if err != nil {
		return nil, err
	}
	defer s.remove()
	for _, kind := range Kinds {
		if err := os.Rename(s.tmp[kind], filepath.Join(dir, filepath.FromSlash(FilePath(kind, month.Key)))); err != nil {
			return nil, errors.Wrap(err, "coldstore: rename")
		}
	}
	if err := syncDir(dir, s.entry.Files); err != nil {
		return nil, err
	}
	return s.entry, nil
}

// stagedMonth is a month's files written and checked at temporary names next to their final paths, and the
// month's entry that describes them.
type stagedMonth struct {
	entry *MonthEntry
	tmp   monthFiles
}

// remove removes the temporary files that are left.
func (s *stagedMonth) remove() {
	for _, f := range s.tmp {
		_ = os.Remove(f)
	}
}

// stageMonth writes the month's files from the tables r and e below dir, at temporary names next to their final
// paths, reads them back and checks them against r and e. Moving them into place is up to the caller, as is
// removing them (stagedMonth.remove); on error, nothing is left. check is writeMonth's.
func (j *job) stageMonth(ctx context.Context, month Month, dir string, recruitStageID int, check func(MonthStats) error) (_ *stagedMonth, err error) {
	src, err := j.sourceStats(ctx, recruitStageID)
	if err != nil {
		return nil, err
	}
	if check != nil {
		if err := check(src); err != nil {
			return nil, err
		}
	}

	tmp := monthFiles{}
	suffix, err := tmpSuffix()
	if err != nil {
		return nil, err
	}
	for _, kind := range Kinds {
		final := filepath.Join(dir, filepath.FromSlash(FilePath(kind, month.Key)))
		tmp[kind] = final + suffix
		if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
			return nil, errors.Wrap(err, "coldstore: create directory")
		}
	}
	defer func() {
		if err != nil {
			for _, f := range tmp {
				_ = os.Remove(f)
			}
		}
	}()

	recruit := strconv.Itoa(recruitStageID)
	kv := func(sort string) string {
		s := "KV_METADATA {month: " + quote(month.Key)
		if sort != "" {
			s += ", sort: " + quote(sort)
		}
		return s
	}
	// The SQL of the validated prototype, with report_id (or the whole key) as the last sort key so that the
	// output does not depend on how ties come out of a parallel sort.
	statements := []string{
		// non-recruit reports: server first so that a server filter skips row groups, then the account for the
		// personal queries
		`COPY (SELECT * FROM r WHERE stage_id <> ` + recruit + ` ORDER BY server, account_id, created_at, report_id)
			TO ` + quote(tmp[KindDropReports]) + ` (` + parquetOptions + `, ` + kv("server,account_id,created_at") + `})`,
		`COPY (SELECT e.* FROM e SEMI JOIN r ON r.report_id = e.report_id AND r.stage_id <> ` + recruit + ` ORDER BY e.report_id)
			TO ` + quote(tmp[KindExtras]) + ` (` + parquetOptions + `, ` + kv("report_id") + `})`,
		// recruit reports carry their extras inline; their stage_id is in the metadata
		`COPY (SELECT r.* EXCLUDE (stage_id), e.ip, e.metadata, e.md5 FROM r LEFT JOIN e USING (report_id)
				WHERE r.stage_id = ` + recruit + ` ORDER BY r.created_at, r.report_id)
			TO ` + quote(tmp[KindRecruit]) + ` (` + parquetOptions + `, ` + kv("created_at") + `, stage_id: ` + quote(recruit) + `})`,
		// hours are UTC hours (the job runs with TimeZone UTC): every server's day boundary is a whole hour
		`COPY (SELECT server, stage_id, pattern_id,
				COALESCE(source_name IN (` + manualSourcesList() + `), false) AS is_manual,
				times = 1 AS one_time, date_trunc('hour', created_at) AS hour,
				count(*)::BIGINT AS cnt, sum(times)::BIGINT AS times
			FROM r WHERE stage_id <> ` + recruit + ` AND reliability = 0 GROUP BY ALL
			ORDER BY hour, server, stage_id, pattern_id, is_manual, one_time)
			TO ` + quote(tmp[KindRollup]) + ` (` + parquetOptions + `, ` + kv("") + `})`,
	}
	for _, s := range statements {
		if err := j.exec(ctx, s); err != nil {
			return nil, err
		}
	}

	if err := j.checkFiles(ctx, tmp, src, recruitStageID); err != nil {
		return nil, errors.Wrapf(err, "coldstore: export of %s does not match the source", month.Key)
	}

	entry := &MonthEntry{
		Month:          month.Key,
		MonthStats:     src,
		RecruitStageID: recruitStageID,
		GeneratedAt:    time.Now().UTC(),
		Generator:      Generator(),
	}
	for _, kind := range Kinds {
		fe, err := describeFile(tmp[kind])
		if err != nil {
			return nil, err
		}
		fe.Path = FilePath(kind, month.Key)
		fe.Rows, err = j.countRows(ctx, tmp[kind])
		if err != nil {
			return nil, err
		}
		entry.Files = append(entry.Files, fe)
	}
	return &stagedMonth{entry: entry, tmp: tmp}, nil
}

// checkFiles reads a month's files back and compares them with the source, and the rollup with the reports.
func (j *job) checkFiles(ctx context.Context, files monthFiles, want MonthStats, recruitStageID int) error {
	got, err := j.fileStats(ctx, files, recruitStageID)
	if err != nil {
		return err
	}
	problems := want.diff(got)
	cnt, times, err := j.rollupTotals(ctx, files[KindRollup])
	if err != nil {
		return err
	}
	if cnt != got.ReliableRows || times != got.ReliableTimes {
		problems = append(problems, fmt.Sprintf("rollup: cnt %d, times %d; reports: reliable_rows %d, reliable_times %d",
			cnt, times, got.ReliableRows, got.ReliableTimes))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (j *job) countRows(ctx context.Context, file string) (int64, error) {
	var n int64
	err := j.queryRow(ctx, "SELECT count(*) FROM read_parquet(["+quote(file)+"])", &n)
	return n, err
}

// ExportDims writes the drop pattern elements below dir. The patterns are append-only, so a newer copy is a
// superset of an older one. The other dimensions (stages, items, time ranges, drop infos) are small tables the app
// reads from PostgreSQL and never deletes from, and drop_patterns only holds the hash the elements already
// describe, so the cold queries need none of them.
//
// archived, if not nil, is the manifest's entry of the file being replaced: a source with fewer elements is older
// than the one the archived months were exported from (e.g. an older restore), and is refused before anything is
// replaced, since the archived months may refer to the patterns it lacks.
func ExportDims(ctx context.Context, opts Options, dir string, archived *FileEntry) (*FileEntry, error) {
	j, err := newJob(ctx, opts, "export-dims", true)
	if err != nil {
		return nil, err
	}
	defer j.Close()

	final := filepath.Join(dir, filepath.FromSlash(DropPatternElementsFile))
	suffix, err := tmpSuffix()
	if err != nil {
		return nil, err
	}
	tmp := final + suffix
	defer os.Remove(tmp)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return nil, errors.Wrap(err, "coldstore: create directory")
	}
	if err := j.exec(ctx, `COPY (SELECT * FROM postgres_query('pg', $$
			SELECT element_id, drop_pattern_id, item_id, quantity FROM drop_pattern_elements$$) ORDER BY element_id)
		TO `+quote(tmp)+` (`+parquetOptions+`, KV_METADATA {sort: 'element_id'})`); err != nil {
		return nil, err
	}
	fe, err := describeFile(tmp)
	if err != nil {
		return nil, err
	}
	fe.Path = DropPatternElementsFile
	if fe.Rows, err = j.countRows(ctx, tmp); err != nil {
		return nil, err
	}
	if archived != nil && fe.Rows < archived.Rows {
		return nil, fmt.Errorf("coldstore: PostgreSQL holds %d drop pattern elements, fewer than the %d of the archived %s: "+
			"it is older than the source of the archive; not replacing it", fe.Rows, archived.Rows, DropPatternElementsFile)
	}
	if err := os.Rename(tmp, final); err != nil {
		return nil, errors.Wrap(err, "coldstore: rename")
	}
	if err := syncDir(dir, []FileEntry{fe}); err != nil {
		return nil, err
	}
	return &fe, nil
}

// describeFile returns the size and SHA-256 of a file, after flushing it to disk.
func describeFile(name string) (FileEntry, error) {
	f, err := os.Open(name)
	if err != nil {
		return FileEntry{}, errors.Wrap(err, "coldstore: open")
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return FileEntry{}, errors.Wrap(err, "coldstore: sync")
	}
	return hashFile(f)
}

// hashFile returns the size and SHA-256 of what is left to read of f.
func hashFile(f *os.File) (FileEntry, error) {
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return FileEntry{}, errors.Wrap(err, "coldstore: read")
	}
	return FileEntry{SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: n}, nil
}

// syncDir flushes the directories of the files, so that their renames survive a crash.
func syncDir(dir string, files []FileEntry) error {
	seen := map[string]bool{}
	for _, fe := range files {
		d := filepath.Dir(filepath.Join(dir, filepath.FromSlash(fe.Path)))
		if seen[d] {
			continue
		}
		seen[d] = true
		f, err := os.Open(d)
		if err != nil {
			return errors.Wrap(err, "coldstore: open directory")
		}
		err = f.Sync()
		f.Close()
		if err != nil {
			return errors.Wrap(err, "coldstore: sync directory")
		}
	}
	return nil
}

// tmpSuffix returns a unique suffix for files being written, so that concurrent writers of the same file (e.g. two
// exports refreshing the dimensions) never write into each other's file.
func tmpSuffix() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", errors.Wrap(err, "coldstore: random suffix")
	}
	return "." + hex.EncodeToString(b) + ".tmp", nil
}
