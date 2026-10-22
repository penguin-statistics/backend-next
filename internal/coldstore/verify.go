package coldstore

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

// VerificationError lists everything a verification found wrong.
type VerificationError struct {
	Subject  string
	Problems []string
}

func (e *VerificationError) Error() string {
	return "coldstore: " + e.Subject + " failed verification: " + strings.Join(e.Problems, "; ")
}

// VerifyMonth checks a month's files in dir against the manifest: each file's size and SHA-256, and the content
// of the files (MonthStats, including the full-column content hashes) against the month's entry, and the rollup's
// totals against the reports. With againstSource, it also reads the month again from PostgreSQL and compares the
// content of the files with it, which is what ties the files to the source: the manifest is only a record of the
// export. It returns a *VerificationError when the files do not pass.
func VerifyMonth(ctx context.Context, opts Options, dir string, m *Manifest, month Month, againstSource bool) error {
	if PendingPatch(dir, month.Key) {
		return fmt.Errorf("coldstore: a patch of %s stopped partway (%s): run coldstore-recover first", month.Key, journalPath(dir, MonthTarget(month.Key)))
	}
	entry := m.Month(month.Key)
	if entry == nil {
		return fmt.Errorf("coldstore: month %s is not in the manifest", month.Key)
	}
	var problems []string
	files := monthFiles{}
	for _, kind := range Kinds {
		p := FilePath(kind, month.Key)
		files[kind] = filepath.Join(dir, filepath.FromSlash(p))
		want := entry.File(kind)
		if want == nil {
			problems = append(problems, p+": not in the manifest")
			continue
		}
		got, err := describeExisting(dir, p)
		if err != nil {
			return err
		}
		if got.Bytes != want.Bytes || got.SHA256 != want.SHA256 {
			problems = append(problems, fmt.Sprintf("%s: %d bytes, sha256 %s; manifest: %d bytes, sha256 %s",
				p, got.Bytes, got.SHA256, want.Bytes, want.SHA256))
		}
	}

	j, err := newJob(ctx, opts, "verify-"+month.Key, againstSource)
	if err != nil {
		return err
	}
	defer j.Close()

	stageID, err := j.kvMetadata(ctx, files[KindRecruit], "stage_id")
	if err != nil {
		return err
	}
	if stageID != strconv.Itoa(entry.RecruitStageID) {
		problems = append(problems, fmt.Sprintf("recruit stage_id %q in the file, %d in the manifest", stageID, entry.RecruitStageID))
	}
	got, err := j.fileStats(ctx, files, entry.RecruitStageID)
	if err != nil {
		return err
	}
	for _, p := range entry.MonthStats.diff(got) {
		problems = append(problems, "files vs manifest: "+p)
	}
	for _, kind := range Kinds {
		want := entry.File(kind)
		if want == nil {
			continue
		}
		n, err := j.countRows(ctx, files[kind])
		if err != nil {
			return err
		}
		if n != want.Rows {
			problems = append(problems, fmt.Sprintf("%s: %d rows, manifest: %d", want.Path, n, want.Rows))
		}
	}
	cnt, times, err := j.rollupTotals(ctx, files[KindRollup])
	if err != nil {
		return err
	}
	if cnt != got.ReliableRows || times != got.ReliableTimes {
		problems = append(problems, fmt.Sprintf("rollup: cnt %d, times %d; reports: reliable_rows %d, reliable_times %d",
			cnt, times, got.ReliableRows, got.ReliableTimes))
	}

	if againstSource {
		srcStageID, err := j.recruitStageID(ctx)
		if err != nil {
			return err
		}
		if srcStageID != entry.RecruitStageID {
			problems = append(problems, fmt.Sprintf("recruit stage_id %d in PostgreSQL, %d in the manifest", srcStageID, entry.RecruitStageID))
		}
		if err := j.loadSource(ctx, month); err != nil {
			return err
		}
		src, err := j.sourceStats(ctx, entry.RecruitStageID)
		if err != nil {
			return err
		}
		for _, p := range src.diff(got) {
			problems = append(problems, "files vs PostgreSQL: "+p)
		}
		if j.orphanExtras > 0 {
			problems = append(problems, fmt.Sprintf("PostgreSQL: %d drop_report_extras rows of the month belong to no report of "+
				"the month and are not archived", j.orphanExtras))
		}
	}

	if len(problems) > 0 {
		return &VerificationError{Subject: "month " + month.Key, Problems: problems}
	}
	return nil
}

// VerifyDims checks the dimension files in dir against the manifest (size and SHA-256) and, with againstSource,
// the drop pattern elements against PostgreSQL: the patterns are append-only, so the source's rows up to the
// largest element_id of the file must hash alike.
func VerifyDims(ctx context.Context, opts Options, dir string, m *Manifest, againstSource bool) error {
	want := m.Dim(DropPatternElementsFile)
	if want == nil {
		return fmt.Errorf("coldstore: %s is not in the manifest", DropPatternElementsFile)
	}
	var problems []string
	got, err := describeExisting(dir, DropPatternElementsFile)
	if err != nil {
		return err
	}
	if got.Bytes != want.Bytes || got.SHA256 != want.SHA256 {
		problems = append(problems, fmt.Sprintf("%s: %d bytes, sha256 %s; manifest: %d bytes, sha256 %s",
			want.Path, got.Bytes, got.SHA256, want.Bytes, want.SHA256))
	}

	if againstSource {
		j, err := newJob(ctx, opts, "verify-dims", true)
		if err != nil {
			return err
		}
		defer j.Close()
		const cols = "element_id, drop_pattern_id, item_id, quantity"
		file := filepath.Join(dir, filepath.FromSlash(DropPatternElementsFile))
		var n int64
		var maxID *int64
		var h string
		if err := j.queryRow(ctx, `SELECT count(*), max(element_id), coalesce(sum(hash(`+cols+`)::HUGEINT), 0)::VARCHAR
			FROM read_parquet([`+quote(file)+`])`, &n, &maxID, &h); err != nil {
			return err
		}
		limit := int64(-1)
		if maxID != nil {
			limit = *maxID
		}
		var srcN int64
		var srcH string
		if err := j.queryRow(ctx, `SELECT count(*), coalesce(sum(hash(`+cols+`)::HUGEINT), 0)::VARCHAR
			FROM postgres_query('pg', $$SELECT `+cols+` FROM drop_pattern_elements WHERE element_id <= `+strconv.FormatInt(limit, 10)+`$$)`,
			&srcN, &srcH); err != nil {
			return err
		}
		if n != srcN || h != srcH {
			problems = append(problems, fmt.Sprintf("%s: %d rows, hash %s; PostgreSQL: %d rows, hash %s", want.Path, n, h, srcN, srcH))
		}
	}

	if len(problems) > 0 {
		return &VerificationError{Subject: DropPatternElementsFile, Problems: problems}
	}
	return nil
}

// errStaleDims is returned by verifyDimsCoverMonth when the drop pattern elements file lacks elements of a pattern
// that a report of the month references.
var errStaleDims = errors.New("coldstore: the drop pattern elements do not cover the month")

// verifyDimsCoverMonth checks that the drop pattern elements file in dir holds every element of the patterns that
// the reports of the month's files reference, which the cold queries join them with. The file was exported before
// the month's files: a pattern created in between (by a late or replayed report of the month) is missing from it.
// The patterns are append-only and a pattern's elements commit with it, before any report of it, and VerifyDims
// checks the file against PostgreSQL up to its largest element_id: so the elements missing from it are those of
// PostgreSQL after that element_id, and the month's files must reference none of their patterns. The files must be
// those of the month in PostgreSQL, with its partitions locked against writes (see Archiver).
func verifyDimsCoverMonth(ctx context.Context, opts Options, dir string, month Month) error {
	j, err := newJob(ctx, opts, "dims-cover-"+month.Key, true)
	if err != nil {
		return err
	}
	defer j.Close()
	var maxID *int64
	if err := j.queryRow(ctx, `SELECT max(element_id) FROM read_parquet([`+
		quote(filepath.Join(dir, filepath.FromSlash(DropPatternElementsFile)))+`])`, &maxID); err != nil {
		return err
	}
	limit := int64(-1)
	if maxID != nil {
		limit = *maxID
	}
	file := func(kind Kind) string {
		return quote(filepath.Join(dir, filepath.FromSlash(FilePath(kind, month.Key))))
	}
	var missing int64
	if err := j.queryRow(ctx, `SELECT count(DISTINCT drop_pattern_id)
		FROM postgres_query('pg', $$SELECT drop_pattern_id FROM drop_pattern_elements WHERE element_id > `+strconv.FormatInt(limit, 10)+`$$)
		WHERE drop_pattern_id IN (SELECT pattern_id FROM read_parquet([`+file(KindDropReports)+`])
			UNION ALL SELECT pattern_id FROM read_parquet([`+file(KindRecruit)+`]))`, &missing); err != nil {
		return err
	}
	if missing > 0 {
		return fmt.Errorf("%w: %d patterns of its reports have elements after element_id %d of %s", errStaleDims, missing, limit,
			DropPatternElementsFile)
	}
	return nil
}

// IsVerificationError tells whether err is a failed verification, as opposed to an error that kept it from
// completing.
func IsVerificationError(err error) bool {
	var v *VerificationError
	return errors.As(err, &v)
}
