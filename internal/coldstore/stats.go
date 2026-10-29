package coldstore

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/pkg/errors"
)

// The columns of drop_reports and drop_report_extras as archived, in this order. The full-column content hashes
// are sum(hash(<all columns>)) over these lists; both sides of a comparison are hashed by DuckDB, so the rows read
// from PostgreSQL and the rows read back from the files hash alike.
const (
	reportColumns = "report_id, stage_id, pattern_id, times, created_at, reliability, server, account_id, source_name, version"
	extrasColumns = "report_id, ip, metadata, md5"
)

// MonthStats summarizes the reports of a month and the extras that belong to them (the drop_report_extras rows
// whose report_id is one of the month's reports). It is computed alike from the rows read from PostgreSQL and
// from the files, so two MonthStats are equal exactly when the files hold the source's rows.
type MonthStats struct {
	// Rows counts the reports, recruit included.
	Rows int64 `json:"rows"`
	// RecruitRows counts the recruit reports.
	RecruitRows int64 `json:"recruit_rows"`
	// ExtrasRows counts the extras of the month's reports.
	ExtrasRows int64 `json:"extras_rows"`
	// ReliableRows counts the non-recruit reports with reliability = 0, the rows the rollup counts.
	ReliableRows int64 `json:"reliable_rows"`
	// ReliableTimes is sum(times) over the ReliableRows reports.
	ReliableTimes int64 `json:"reliable_times"`
	// SumTimes is sum(times) over all reports.
	SumTimes int64 `json:"sum_times"`

	MinReportID  *int64     `json:"min_report_id,omitempty"`
	MaxReportID  *int64     `json:"max_report_id,omitempty"`
	MinCreatedAt *time.Time `json:"min_created_at,omitempty"`
	MaxCreatedAt *time.Time `json:"max_created_at,omitempty"`

	// ReportsHash is sum(hash(reportColumns)) over all reports (recruit ones with their stage_id), as a decimal
	// HUGEINT.
	ReportsHash string `json:"reports_hash"`
	// ExtrasHash is sum(hash(extrasColumns)) over the extras, as a decimal HUGEINT.
	ExtrasHash string `json:"extras_hash"`
}

// diff lists the fields in which got differs from want.
func (want MonthStats) diff(got MonthStats) []string {
	var out []string
	add := func(name string, w, g any) {
		out = append(out, fmt.Sprintf("%s: got %v, want %v", name, g, w))
	}
	ints := []struct {
		name string
		w, g int64
	}{
		{"rows", want.Rows, got.Rows},
		{"recruit_rows", want.RecruitRows, got.RecruitRows},
		{"extras_rows", want.ExtrasRows, got.ExtrasRows},
		{"reliable_rows", want.ReliableRows, got.ReliableRows},
		{"reliable_times", want.ReliableTimes, got.ReliableTimes},
		{"sum_times", want.SumTimes, got.SumTimes},
	}
	for _, c := range ints {
		if c.w != c.g {
			add(c.name, c.w, c.g)
		}
	}
	if s, t := fmtInt(want.MinReportID), fmtInt(got.MinReportID); s != t {
		add("min_report_id", s, t)
	}
	if s, t := fmtInt(want.MaxReportID), fmtInt(got.MaxReportID); s != t {
		add("max_report_id", s, t)
	}
	if s, t := fmtTime(want.MinCreatedAt), fmtTime(got.MinCreatedAt); s != t {
		add("min_created_at", s, t)
	}
	if s, t := fmtTime(want.MaxCreatedAt), fmtTime(got.MaxCreatedAt); s != t {
		add("max_created_at", s, t)
	}
	if want.ReportsHash != got.ReportsHash {
		add("reports_hash", want.ReportsHash, got.ReportsHash)
	}
	if want.ExtrasHash != got.ExtrasHash {
		add("extras_hash", want.ExtrasHash, got.ExtrasHash)
	}
	return out
}

func fmtInt(p *int64) string {
	if p == nil {
		return "NULL"
	}
	return strconv.FormatInt(*p, 10)
}

func fmtTime(p *time.Time) string {
	if p == nil {
		return "NULL"
	}
	return p.UTC().Format(time.RFC3339Nano)
}

// monthStats computes MonthStats over a relation of reports (reportColumns) and one of their extras
// (extrasColumns). Each relation is aggregated in a subquery of its own, which reads it once, as a stream: a CTE
// referenced twice is materialized, which held the extras of a whole month (148M rows of 2023-10, 11 GB spilled
// under a 1.5 GB memory limit) and overflowed the temp directory.
func (j *job) monthStats(ctx context.Context, reports, extras string, recruitStageID int) (MonthStats, error) {
	var (
		s                 MonthStats
		minID, maxID      sql.NullInt64
		minTime, maxTime  sql.NullTime
		recruit           = strconv.Itoa(recruitStageID)
		reliable          = "stage_id <> " + recruit + " AND reliability = 0"
		reportsH, extrasH string
	)
	err := j.queryRow(ctx, `SELECT * FROM (
			SELECT count(*), count(*) FILTER (WHERE stage_id = `+recruit+`),
				count(*) FILTER (WHERE `+reliable+`), coalesce(sum(times) FILTER (WHERE `+reliable+`), 0)::BIGINT,
				coalesce(sum(times), 0)::BIGINT,
				min(report_id), max(report_id), min(created_at), max(created_at),
				coalesce(sum(hash(`+reportColumns+`)::HUGEINT), 0)::VARCHAR
			FROM (`+reports+`)
		), (
			SELECT count(*), coalesce(sum(hash(`+extrasColumns+`)::HUGEINT), 0)::VARCHAR
			FROM (`+extras+`)
		)`,
		&s.Rows, &s.RecruitRows, &s.ReliableRows, &s.ReliableTimes, &s.SumTimes,
		&minID, &maxID, &minTime, &maxTime, &reportsH, &s.ExtrasRows, &extrasH)
	if err != nil {
		return s, err
	}
	s.ReportsHash, s.ExtrasHash = reportsH, extrasH
	if minID.Valid {
		s.MinReportID, s.MaxReportID = &minID.Int64, &maxID.Int64
	}
	if minTime.Valid {
		lo, hi := minTime.Time.UTC(), maxTime.Time.UTC()
		s.MinCreatedAt, s.MaxCreatedAt = &lo, &hi
	}
	return s, nil
}

// monthFiles are the paths of one month's files.
type monthFiles map[Kind]string

// fileStats computes MonthStats over a month's files: the reports are drop_reports ∪ recruit (with the recruit
// stage_id put back), the extras are drop_report_extras ∪ the recruit rows' extras columns (recruit rows without
// extras have a NULL ip, which drop_report_extras never has).
func (j *job) fileStats(ctx context.Context, files monthFiles, recruitStageID int) (MonthStats, error) {
	return j.monthStats(ctx, fileReports(files, recruitStageID), fileExtras(files), recruitStageID)
}

// fileReports is the query of a month's reports (reportColumns) from its files: drop_reports ∪ recruit, with the
// recruit stage_id put back.
func fileReports(files monthFiles, recruitStageID int) string {
	return `SELECT ` + reportColumns + ` FROM read_parquet([` + quote(files[KindDropReports]) + `])
		UNION ALL SELECT report_id, ` + strconv.Itoa(recruitStageID) + `::INTEGER AS stage_id, pattern_id, times,
			created_at, reliability, server, account_id, source_name, version
		FROM read_parquet([` + quote(files[KindRecruit]) + `])`
}

// fileExtras is the query of the extras (extrasColumns) of a month's reports from its files: drop_report_extras ∪
// the recruit rows' extras columns.
func fileExtras(files monthFiles) string {
	return `SELECT ` + extrasColumns + ` FROM read_parquet([` + quote(files[KindExtras]) + `])
		UNION ALL SELECT ` + extrasColumns + ` FROM read_parquet([` + quote(files[KindRecruit]) + `]) WHERE ip IS NOT NULL`
}

// rollupTotals returns sum(cnt) and sum(times) of a rollup file, which must equal ReliableRows and ReliableTimes.
func (j *job) rollupTotals(ctx context.Context, file string) (cnt, times int64, err error) {
	err = j.queryRow(ctx, `SELECT coalesce(sum(cnt), 0)::BIGINT, coalesce(sum(times), 0)::BIGINT
		FROM read_parquet([`+quote(file)+`])`, &cnt, &times)
	return cnt, times, err
}

// recruitStageID resolves the stage_id of the recruit stage in PostgreSQL, as the app does (by its ark stage id).
func (j *job) recruitStageID(ctx context.Context) (int, error) {
	var id sql.NullInt32
	if err := j.queryRow(ctx, `SELECT max(stage_id) FROM postgres_query('pg', $$
		SELECT stage_id FROM stages WHERE ark_stage_id = 'recruit'$$)`, &id); err != nil {
		return 0, err
	}
	if !id.Valid {
		return 0, errors.New("coldstore: stage 'recruit' not found in PostgreSQL")
	}
	return int(id.Int32), nil
}

// loadSource reads a month's reports into the table r and their extras into the table e. The extras are read by
// the report_id range of the month (drop_report_extras is keyed by report_id alone) and kept only for the month's
// reports; extras without a report are never archived (see countOrphanExtras).
func (j *job) loadSource(ctx context.Context, month Month) error {
	where := "created_at < " + pgTime(month.End)
	if !month.IsPre() {
		where = "created_at >= " + pgTime(month.Start) + " AND " + where
	}
	if err := j.exec(ctx, `CREATE TABLE r AS SELECT * FROM postgres_query('pg', $$
		SELECT report_id, stage_id, pattern_id, times, created_at, reliability, server::text AS server,
			account_id, source_name, version
		FROM drop_reports WHERE `+where+`$$)`); err != nil {
		return err
	}
	var lo, hi sql.NullInt64
	if err := j.queryRow(ctx, "SELECT min(report_id), max(report_id) FROM r", &lo, &hi); err != nil {
		return err
	}
	if !lo.Valid {
		// no reports: an empty range, so that e still gets the source's column types
		lo.Int64, hi.Int64 = 0, -1
	}
	if err := j.exec(ctx, fmt.Sprintf(`CREATE TABLE e AS SELECT x.* FROM postgres_query('pg', $$
		SELECT report_id, ip::text AS ip, metadata::text AS metadata, md5
		FROM drop_report_extras WHERE report_id BETWEEN %d AND %d$$) x
		SEMI JOIN r ON r.report_id = x.report_id`, lo.Int64, hi.Int64)); err != nil {
		return err
	}
	return j.countOrphanExtras(ctx, where)
}

// countOrphanExtras sets orphanExtras, after loadSource filled r, on the schema of the new database of #752
// (scripts/newdb), where drop_report_extras has the created_at of its report and is partitioned by it like
// drop_reports: it counts the rows of the month's drop_report_extras partition (where selects the month by
// created_at) that belong to none of the month's reports. Those are not archived, and the monthly archiving would
// lose them with the partition (see VerifyMonth). On a database whose drop_report_extras has no created_at (today's,
// which the backfill reads), orphanExtras is -1 and nothing more is read.
func (j *job) countOrphanExtras(ctx context.Context, where string) error {
	j.orphanExtras = -1
	var partitioned bool
	if err := j.queryRow(ctx, `SELECT * FROM postgres_query('pg', $$SELECT EXISTS (SELECT FROM pg_attribute
		WHERE attrelid = to_regclass('drop_report_extras') AND attname = 'created_at' AND NOT attisdropped)$$)`, &partitioned); err != nil {
		return err
	}
	if !partitioned {
		return nil
	}
	return j.queryRow(ctx, `SELECT count(*) FROM postgres_query('pg', $$
		SELECT report_id FROM drop_report_extras WHERE `+where+`$$) x
		ANTI JOIN r ON r.report_id = x.report_id`, &j.orphanExtras)
}

// sourceStats computes MonthStats over the tables loadSource filled.
func (j *job) sourceStats(ctx context.Context, recruitStageID int) (MonthStats, error) {
	return j.monthStats(ctx, "SELECT "+reportColumns+" FROM r", "SELECT "+extrasColumns+" FROM e", recruitStageID)
}

// pgTime returns t as a PostgreSQL timestamptz literal.
func pgTime(t time.Time) string {
	return "'" + t.UTC().Format("2006-01-02 15:04:05") + "+00'"
}
