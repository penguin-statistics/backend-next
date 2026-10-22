package coldstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// ProdCheckOptions bounds the load ProdCheck puts on the production database.
type ProdCheckOptions struct {
	// StatementTimeout is the statement_timeout of the session (default 60s).
	StatementTimeout time.Duration
	// Pause is the time to wait between two statements.
	Pause time.Duration
	// Reliability also counts the reliable rows (non-recruit, reliability = 0), which catches reliability changes
	// but reads the table instead of only the created_at index: much slower.
	Reliability bool
}

// DayCount is the number of reports of one UTC day.
type DayCount struct {
	Rows         int64
	ReliableRows int64
}

// MonthCheck compares the reports of a month in the production database with the month's manifest entry.
type MonthCheck struct {
	Month                string
	ManifestRows         int64
	SourceRows           int64
	ManifestReliableRows int64
	// SourceReliableRows is only counted with ProdCheckOptions.Reliability.
	SourceReliableRows int64
	// Days are the database's counts per UTC day ("YYYY-MM-DD").
	Days map[string]DayCount
	Took time.Duration
}

// Mismatch tells whether the database disagrees with the manifest.
func (c MonthCheck) Mismatch(reliability bool) bool {
	return c.SourceRows != c.ManifestRows || (reliability && c.SourceReliableRows != c.ManifestReliableRows)
}

// ProdCheck counts the reports of archived months in PostgreSQL, one statement per UTC day on one connection, and
// compares them with the manifest: a month the database holds more or fewer reports of than its files gained or
// lost rows after the export (the files then have to be regenerated, see PatchMonth, or the month exported again).
// Only counts are compared, which the created_at index answers. The app never deletes reports, and its only change
// to existing ones, a reliability change, keeps the count: only o.Reliability catches it, by counting the reliable
// rows too.
//
// The session is read-only, without parallel workers or JIT and with o.StatementTimeout, so that it can run on the
// production database (a day of the busiest month, 4.2M rows, takes 2 s on the local restore); run it off-peak. A month whose counting fails (e.g. a statement timed out) ends the check.
func ProdCheck(ctx context.Context, db *sql.DB, m *Manifest, months []string, o ProdCheckOptions, report func(MonthCheck)) error {
	if o.StatementTimeout <= 0 {
		o.StatementTimeout = time.Minute
	}
	if len(months) == 0 {
		for _, e := range m.Months {
			months = append(months, e.Month)
		}
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return errors.Wrap(err, "coldstore: connect to PostgreSQL")
	}
	defer func() {
		// the session's settings must not reach the other users of db: close the connection instead of returning it
		// to the pool
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		conn.Close()
	}()
	for _, s := range []string{
		"SET statement_timeout = " + strconv.FormatInt(o.StatementTimeout.Milliseconds(), 10),
		"SET default_transaction_read_only = on",
		"SET max_parallel_workers_per_gather = 0",
		"SET jit = off",
	} {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			return errors.Wrapf(err, "coldstore: %s", s)
		}
	}

	for _, key := range months {
		entry := m.Month(key)
		if entry == nil {
			return fmt.Errorf("coldstore: month %s is not in the manifest", key)
		}
		month, err := ParseMonth(key)
		if err != nil {
			return err
		}
		started := time.Now()
		c := MonthCheck{Month: key, ManifestRows: entry.Rows, ManifestReliableRows: entry.ReliableRows, Days: map[string]DayCount{}}
		for _, b := range batches(month) {
			if err := countDays(ctx, conn, b[0], b[1], entry.RecruitStageID, o.Reliability, c.Days); err != nil {
				return errors.Wrapf(err, "coldstore: count %s before %s", key, b[1].Format(time.DateOnly))
			}
			if o.Pause > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(o.Pause):
				}
			}
		}
		for _, d := range c.Days {
			c.SourceRows += d.Rows
			c.SourceReliableRows += d.ReliableRows
		}
		c.Took = time.Since(started)
		log.Info().Str("month", key).Int64("manifestRows", c.ManifestRows).Int64("sourceRows", c.SourceRows).
			Bool("mismatch", c.Mismatch(o.Reliability)).Dur("took", c.Took).Msg("coldstore: checked month against PostgreSQL")
		report(c)
	}
	return nil
}

// batches splits a month in UTC days; the pre month is one range, without a start.
func batches(month Month) [][2]time.Time {
	if month.IsPre() {
		return [][2]time.Time{{time.Time{}, month.End}}
	}
	var out [][2]time.Time
	for lo := month.Start; lo.Before(month.End); lo = lo.AddDate(0, 0, 1) {
		out = append(out, [2]time.Time{lo, lo.AddDate(0, 0, 1)})
	}
	return out
}

// countDays adds the reports with lo <= created_at < hi (no lower bound if lo is zero) to days, per UTC day. A
// whole day is counted without grouping: a GROUP BY on an expression is estimated as millions of groups, which
// plans a spilling hash aggregate.
func countDays(ctx context.Context, conn *sql.Conn, lo, hi time.Time, recruitStageID int, reliability bool, days map[string]DayCount) error {
	reliable := "0"
	if reliability {
		reliable = "count(*) FILTER (WHERE reliability = 0 AND stage_id <> " + strconv.Itoa(recruitStageID) + ")"
	}
	query := `SELECT to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), count(*), ` + reliable + `
		FROM drop_reports WHERE created_at < ` + pgTime(hi) + ` GROUP BY 1`
	if !lo.IsZero() {
		query = `SELECT ` + quote(lo.Format(time.DateOnly)) + `, count(*), ` + reliable + `
			FROM drop_reports WHERE created_at >= ` + pgTime(lo) + ` AND created_at < ` + pgTime(hi) + ` HAVING count(*) > 0`
	}
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var day string
		var d DayCount
		if err := rows.Scan(&day, &d.Rows, &d.ReliableRows); err != nil {
			return err
		}
		days[day] = d
	}
	return rows.Err()
}

// FileDayCounts counts the reports of an archived month per UTC day from its files in dir, to compare with
// MonthCheck.Days.
func FileDayCounts(ctx context.Context, opts Options, dir string, entry *MonthEntry) (map[string]DayCount, error) {
	j, err := newJob(ctx, opts, "days-"+entry.Month, false)
	if err != nil {
		return nil, err
	}
	defer j.Close()
	q := `SELECT strftime(created_at, '%Y-%m-%d'), count(*),
			count(*) FILTER (WHERE reliability = 0 AND stage_id <> ` + strconv.Itoa(entry.RecruitStageID) + `)
		FROM (` + fileReports(filesOf(dir, entry.Month), entry.RecruitStageID) + `) GROUP BY 1`
	out := map[string]DayCount{}
	err = j.query(ctx, q, func(rows *sql.Rows) error {
		var day string
		var d DayCount
		if err := rows.Scan(&day, &d.Rows, &d.ReliableRows); err != nil {
			return err
		}
		out[day] = d
		return nil
	})
	return out, err
}

// DayDiff is a day on which two DayCounts disagree.
type DayDiff struct {
	Day       string
	Want, Got DayCount
}

// DiffDays lists the days on which got differs from want, sorted; reliable rows are compared only with reliability.
func DiffDays(want, got map[string]DayCount, reliability bool) []DayDiff {
	var out []DayDiff
	for day := range union(want, got) {
		w, g := want[day], got[day]
		if !reliability {
			w.ReliableRows, g.ReliableRows = 0, 0
		}
		if w != g {
			out = append(out, DayDiff{Day: day, Want: w, Got: g})
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Day < out[k].Day })
	return out
}

func union(a, b map[string]DayCount) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}
