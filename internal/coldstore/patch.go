package coldstore

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// Patch is a change to archived reports, made after an admin fix to reports that are only in the cold storage (or
// that were archived before the fix): reports deleted, or their reliability changed (the app's "delete" sets it to
// -1).
type Patch struct {
	// Delete lists the reports to remove, with their extras.
	Delete []int64
	// Reliability maps reports to their new reliability.
	Reliability map[int64]int
}

// IDs returns the report ids the patch changes, sorted.
func (p Patch) IDs() []int64 {
	ids := make([]int64, 0, len(p.Delete)+len(p.Reliability))
	ids = append(ids, p.Delete...)
	for id := range p.Reliability {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, k int) bool { return ids[i] < ids[k] })
	return ids
}

// Validate checks that the patch changes something and changes no report twice.
func (p Patch) Validate() error {
	if len(p.Delete) == 0 && len(p.Reliability) == 0 {
		return errors.New("coldstore: empty patch")
	}
	seen := map[int64]bool{}
	for _, id := range p.Delete {
		if seen[id] {
			return fmt.Errorf("coldstore: report %d is deleted twice", id)
		}
		seen[id] = true
		if _, ok := p.Reliability[id]; ok {
			return fmt.Errorf("coldstore: report %d is both deleted and given a reliability", id)
		}
	}
	return nil
}

// Only returns the part of the patch that changes the given reports.
func (p Patch) Only(ids []int64) Patch {
	keep := map[int64]bool{}
	for _, id := range ids {
		keep[id] = true
	}
	out := Patch{Reliability: map[int64]int{}}
	for _, id := range p.Delete {
		if keep[id] {
			out.Delete = append(out.Delete, id)
		}
	}
	for id, r := range p.Reliability {
		if keep[id] {
			out.Reliability[id] = r
		}
	}
	return out
}

// PatchResult is the outcome of PatchMonth.
type PatchResult struct {
	// Found lists the patch's reports that are in the month, sorted.
	Found []int64
	// Deleted counts the reports removed; Updated, those whose reliability changed (a report that already had the
	// new reliability is not counted).
	Deleted, Updated int64
	// Entry is the month's new manifest entry, recorded in the manifest, or nil if nothing changed or it was a dry
	// run.
	Entry *MonthEntry
}

// LocateReports finds which archived months hold the given reports, among the months of the manifest whose
// report_id range covers them (or among the given months). It returns the reports found by month key.
func LocateReports(ctx context.Context, opts Options, dir string, m *Manifest, months []string, ids []int64) (map[string][]int64, error) {
	if len(months) == 0 {
		for _, e := range m.Months {
			if e.MinReportID == nil {
				continue
			}
			for _, id := range ids {
				if id >= *e.MinReportID && id <= *e.MaxReportID {
					months = append(months, e.Month)
					break
				}
			}
		}
	}
	found := map[string][]int64{}
	if len(months) == 0 || len(ids) == 0 {
		return found, nil
	}

	uniq := map[int64]bool{}
	for _, id := range ids {
		uniq[id] = true
	}
	del := make([]int64, 0, len(uniq))
	for id := range uniq {
		del = append(del, id)
	}

	j, err := newJob(ctx, opts, "locate", false)
	if err != nil {
		return nil, err
	}
	defer j.Close()
	if err := j.loadIDs(ctx, del, nil); err != nil {
		return nil, err
	}
	for _, key := range months {
		entry := m.Month(key)
		if entry == nil {
			return nil, fmt.Errorf("coldstore: month %s is not in the manifest", key)
		}
		got, err := j.int64s(ctx, `SELECT report_id FROM (`+fileReports(filesOf(dir, key), entry.RecruitStageID)+`)
			SEMI JOIN patch USING (report_id) ORDER BY report_id`)
		if err != nil {
			return nil, err
		}
		if len(got) > 0 {
			found[key] = got
		}
	}
	return found, nil
}

// PatchMonth applies a patch to an archived month of dir: it reads the month's files back (after checking them
// against dir's manifest), applies the patch and writes all the month's files again, rollup included, with the
// exporter's SQL (see stageMonth), then publishes them with the month's new manifest entry as a new generation that
// can be completed or undone from the directory alone if it stops partway (see publishGeneration). It holds the
// month's lock throughout, and first completes or undoes a previous patch of the month that stopped partway. Reports
// of the patch that are not in the month are ignored (see PatchResult.Found). With dryRun, or when nothing changes,
// the files are left untouched.
//
// The month's files are only compared with the manifest, not with PostgreSQL: they are the only copy of the month
// once its partition is dropped. While PostgreSQL still holds the month, VerifyMonth against the source tells
// whether the patched files and the fixed database agree.
func PatchMonth(ctx context.Context, opts Options, dir string, month Month, p Patch, dryRun bool) (*PatchResult, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	target := MonthTarget(month.Key)
	// a dry run writes nothing, so that it works on a read-only copy: a shared lock, which it does not create
	lock := lockTarget
	if dryRun {
		lock = lockTargetShared
	}
	unlock, err := lock(dir, target)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if dryRun {
		if PendingPatch(dir, month.Key) {
			return nil, fmt.Errorf("coldstore: a patch of %s stopped partway: run coldstore-recover (or the patch) first", month.Key)
		}
	} else if rec, err := recoverTarget(dir, target); err != nil {
		return nil, err
	} else if rec != RecoveryNone {
		log.Warn().Str("month", month.Key).Str("recovery", string(rec)).Msg("coldstore: recovered a patch that had stopped partway")
	}
	m, err := ReadManifest(filepath.Join(dir, ManifestFile))
	if err != nil {
		return nil, err
	}
	entry := m.Month(month.Key)
	if entry == nil {
		return nil, fmt.Errorf("coldstore: month %s is not in the manifest", month.Key)
	}
	started := time.Now()
	files := filesOf(dir, month.Key)
	var problems []string
	for _, kind := range Kinds {
		want := entry.File(kind)
		if want == nil {
			return nil, fmt.Errorf("coldstore: %s is not in the manifest", FilePath(kind, month.Key))
		}
		got, err := describeExisting(dir, want.Path)
		if err != nil {
			return nil, err
		}
		if got.Bytes != want.Bytes || got.SHA256 != want.SHA256 {
			problems = append(problems, fmt.Sprintf("%s: %d bytes, sha256 %s; manifest: %d bytes, sha256 %s",
				want.Path, got.Bytes, got.SHA256, want.Bytes, want.SHA256))
		}
	}
	if len(problems) > 0 {
		return nil, &VerificationError{Subject: "month " + month.Key, Problems: problems}
	}

	j, err := newJob(ctx, opts, "patch-"+month.Key, false)
	if err != nil {
		return nil, err
	}
	defer j.Close()

	// the tables loadSource fills from PostgreSQL, from the files instead
	if err := j.exec(ctx, `CREATE TABLE r AS `+fileReports(files, entry.RecruitStageID)); err != nil {
		return nil, err
	}
	if err := j.exec(ctx, `CREATE TABLE e AS `+fileExtras(files)); err != nil {
		return nil, err
	}
	loaded, err := j.sourceStats(ctx, entry.RecruitStageID)
	if err != nil {
		return nil, err
	}
	if diff := entry.MonthStats.diff(loaded); len(diff) > 0 {
		for i := range diff {
			diff[i] = "files vs manifest: " + diff[i]
		}
		return nil, &VerificationError{Subject: "month " + month.Key, Problems: diff}
	}

	if err := j.loadIDs(ctx, p.Delete, p.Reliability); err != nil {
		return nil, err
	}
	res := &PatchResult{}
	if res.Found, err = j.int64s(ctx, `SELECT report_id FROM patch SEMI JOIN r USING (report_id) ORDER BY report_id`); err != nil {
		return nil, err
	}
	if err := j.queryRow(ctx, `SELECT count(*) FROM r SEMI JOIN patch p ON p.report_id = r.report_id AND p.del`, &res.Deleted); err != nil {
		return nil, err
	}
	if err := j.queryRow(ctx, `SELECT count(*) FROM r JOIN patch p ON p.report_id = r.report_id AND NOT p.del
		WHERE r.reliability <> p.reliability`, &res.Updated); err != nil {
		return nil, err
	}
	logger := log.Info().Str("month", month.Key).Int("found", len(res.Found)).Int64("deleted", res.Deleted).
		Int64("updated", res.Updated).Bool("dryRun", dryRun)
	if dryRun {
		logger.Dur("took", time.Since(started)).Msg("coldstore: patch dry run, files left untouched")
		return res, nil
	}
	if res.Deleted+res.Updated == 0 {
		logger.Dur("took", time.Since(started)).Msg("coldstore: patch changes nothing, files left untouched")
		return res, nil
	}

	for _, s := range []string{
		`DELETE FROM e WHERE report_id IN (SELECT report_id FROM patch WHERE del)`,
		`DELETE FROM r WHERE report_id IN (SELECT report_id FROM patch WHERE del)`,
		`UPDATE r SET reliability = p.reliability FROM patch p WHERE p.report_id = r.report_id AND NOT p.del`,
	} {
		if err := j.exec(ctx, s); err != nil {
			return nil, err
		}
	}
	staged, err := j.stageMonth(ctx, month, dir, entry.RecruitStageID, nil)
	if err != nil {
		return nil, err
	}
	defer staged.remove()
	if err := publishGeneration(dir, entry, staged); err != nil {
		return nil, err
	}
	res.Entry = staged.entry
	logger.Int64("rows", res.Entry.Rows).Dur("took", time.Since(started)).Msg("coldstore: patched month")
	return res, nil
}

// loadIDs fills the table patch (report_id, del, reliability) with the reports to delete and those to give a
// reliability.
func (j *job) loadIDs(ctx context.Context, del []int64, reliability map[int64]int) error {
	if err := j.exec(ctx, `CREATE TABLE patch (report_id BIGINT PRIMARY KEY, del BOOLEAN NOT NULL, reliability INTEGER)`); err != nil {
		return err
	}
	rows := make([]string, 0, len(del)+len(reliability))
	for _, id := range del {
		rows = append(rows, "("+strconv.FormatInt(id, 10)+", true, NULL)")
	}
	for id, r := range reliability {
		rows = append(rows, "("+strconv.FormatInt(id, 10)+", false, "+strconv.Itoa(r)+")")
	}
	const batch = 5000
	for len(rows) > 0 {
		n := min(batch, len(rows))
		if err := j.exec(ctx, "INSERT INTO patch VALUES "+strings.Join(rows[:n], ", ")); err != nil {
			return err
		}
		rows = rows[n:]
	}
	return nil
}

func (j *job) int64s(ctx context.Context, query string) ([]int64, error) {
	var out []int64
	err := j.query(ctx, query, func(rows *sql.Rows) error {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return err
		}
		out = append(out, v)
		return nil
	})
	return out, err
}

// filesOf returns the paths of a month's files in dir.
func filesOf(dir, key string) monthFiles {
	files := monthFiles{}
	for _, kind := range Kinds {
		files[kind] = filepath.Join(dir, filepath.FromSlash(FilePath(kind, key)))
	}
	return files
}
