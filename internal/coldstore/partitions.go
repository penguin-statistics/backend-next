package coldstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// ReportTables are the tables of reports that the new database of #752 (scripts/newdb) partitions by month of
// created_at, and whose partitions are dropped once their month is archived. drop_reports holds every report, the
// recruitment reports included, exactly as in today's database.
var ReportTables = []string{"drop_reports", "drop_report_extras"}

// PartitionRunway is how many months after the current one get their partitions ahead of time (see
// CreatePartitions): there is no default partition, so a report of a month without partitions fails to store.
const PartitionRunway = 2

// Partition is a partition of one of the ReportTables that holds exactly one month.
type Partition struct {
	// Parent is the partitioned table (one of ReportTables), Schema and Name the partition's.
	Parent, ParentSchema string
	Schema, Name         string
	Month                Month
}

// QualifiedName returns the partition's name as an SQL identifier with its schema.
func (p Partition) QualifiedName() string {
	return quoteIdent(p.Schema) + "." + quoteIdent(p.Name)
}

// QualifiedParent returns the partitioned table's name as an SQL identifier with its schema.
func (p Partition) QualifiedParent() string {
	return quoteIdent(p.ParentSchema) + "." + quoteIdent(p.Parent)
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// partitionBound matches the partition bound of a range partition as pg_get_expr prints it.
var partitionBound = regexp.MustCompile(`^FOR VALUES FROM \('([^']*)'\) TO \('([^']*)'\)$`)

// MonthPartitions lists the partitions of the ReportTables (those of the search_path that exist) by month key, the
// months sorted. A partition counts only if its bounds are exactly those of a UTC month from Epoch on: any other
// partition (a default one, other bounds) is never archived nor dropped, and is listed in others.
func MonthPartitions(ctx context.Context, db *sql.DB) (months []string, parts map[string][]Partition, others []string, err error) {
	parents := make([]string, len(ReportTables))
	for i, t := range ReportTables {
		parents[i] = "to_regclass('" + t + "')"
	}
	rows, err := db.QueryContext(ctx, `SELECT pn.nspname, p.relname, n.nspname, c.relname, pg_get_expr(c.relpartbound, c.oid)
		FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_class p ON p.oid = i.inhparent
		JOIN pg_namespace pn ON pn.oid = p.relnamespace
		WHERE i.inhparent IN (`+strings.Join(parents, ", ")+`)
		ORDER BY p.relname, c.relname`)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "coldstore: list partitions")
	}
	defer rows.Close()
	parts = map[string][]Partition{}
	for rows.Next() {
		var p Partition
		var bound string
		if err := rows.Scan(&p.ParentSchema, &p.Parent, &p.Schema, &p.Name, &bound); err != nil {
			return nil, nil, nil, errors.Wrap(err, "coldstore: list partitions")
		}
		m, ok := boundMonth(bound)
		if !ok {
			others = append(others, p.Schema+"."+p.Name+" "+bound)
			continue
		}
		p.Month = m
		if parts[m.Key] == nil {
			months = append(months, m.Key)
		}
		parts[m.Key] = append(parts[m.Key], p)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, errors.Wrap(err, "coldstore: list partitions")
	}
	sort.Slice(months, func(i, k int) bool { return monthLess(months[i], months[k]) })
	return months, parts, others, nil
}

// boundMonth returns the month whose bounds a partition bound has, if any.
func boundMonth(bound string) (Month, bool) {
	sub := partitionBound.FindStringSubmatch(bound)
	if sub == nil {
		return Month{}, false
	}
	from, ok1 := parsePGTime(sub[1])
	to, ok2 := parsePGTime(sub[2])
	if !ok1 || !ok2 {
		return Month{}, false
	}
	m := MonthOf(from)
	if m.IsPre() || !m.Start.Equal(from) || !m.End.Equal(to) {
		return Month{}, false
	}
	return m, true
}

// parsePGTime parses a timestamptz as PostgreSQL prints it (DateStyle ISO), in the session's time zone.
func parsePGTime(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05.999999-07", "2006-01-02 15:04:05.999999-07:00", "2006-01-02 15:04:05.999999-07:00:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// CreatePartitions creates the partitions of the current month and of the PartitionRunway months after it that
// are missing, with create_report_partitions of scripts/newdb/schema.sql. Creating a partition locks its
// partitioned table (ACCESS EXCLUSIVE), which every query of it waits for: it waits at most lockTimeout for the lock,
// and fails otherwise (the next run tries again). It does nothing, and returns false, on a database without that
// function (today's, whose reports are not partitioned).
func CreatePartitions(ctx context.Context, db *sql.DB, now time.Time, lockTimeout time.Duration) (bool, error) {
	var exists bool
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT to_regprocedure('create_report_partitions(date)') IS NOT NULL,
		(SELECT count(*) FROM unnest(ARRAY['`+strings.Join(ReportTables, "', '")+`']) t WHERE to_regclass(t) IS NOT NULL)`).
		Scan(&exists, &tables); err != nil {
		return false, errors.Wrap(err, "coldstore: look up create_report_partitions")
	}
	if !exists {
		return false, nil
	}
	_, parts, _, err := MonthPartitions(ctx, db)
	if err != nil {
		return true, err
	}
	var missing []string
	m := MonthOf(now)
	for i := 0; i <= PartitionRunway; i++ {
		start := m.Start.AddDate(0, i, 0)
		if len(parts[start.Format("2006-01")]) < tables {
			missing = append(missing, start.Format("2006-01-02"))
		}
	}
	if len(missing) == 0 {
		return true, nil
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return true, errors.Wrap(err, "coldstore: connect to PostgreSQL")
	}
	defer func() {
		// the session's settings must not reach the other users of db
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		conn.Close()
	}()
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("SET lock_timeout = %d", lockTimeout.Milliseconds())); err != nil {
		return true, errors.Wrap(err, "coldstore: set lock_timeout")
	}
	for _, d := range missing {
		if _, err := conn.ExecContext(ctx, `SELECT create_report_partitions(date '`+d+`')`); err != nil {
			return true, errors.Wrapf(err, "coldstore: create the partitions of %s", d[:7])
		}
		log.Info().Str("month", d[:7]).Msg("coldstore: created the partitions of the month")
	}
	return true, nil
}

// lockOrder returns the qualified names of a month's partitions in the order of ReportTables (the reports before
// their extras), which is the order the report worker writes them in (reportwkr persists a report, then its extra):
// locking them in another order (MonthPartitions lists them by name, extras first) could deadlock with a late writer
// of the month that holds the report partition and then asks for the extras one.
func lockOrder(parts []Partition) []string {
	rank := func(p Partition) int {
		for i, t := range ReportTables {
			if p.Parent == t {
				return i
			}
		}
		return len(ReportTables)
	}
	sorted := append([]Partition{}, parts...)
	sort.SliceStable(sorted, func(i, k int) bool { return rank(sorted[i]) < rank(sorted[k]) })
	names := make([]string, len(sorted))
	for i, p := range sorted {
		names[i] = p.QualifiedName()
	}
	return names
}

// errLockWaiters is returned by dropPartitions when a query waits for the locks the transaction holds on the
// partitions.
var errLockWaiters = errors.New("coldstore: queries wait for the locks on the partitions")

// dropPartitions detaches and drops partitions in tx, which must hold them locked (see Archiver). DETACH takes an
// ACCESS EXCLUSIVE lock on the partitioned tables, which every query of them waits for: tx's lock_timeout bounds the
// wait, so that the app's queries are not held up behind a long one. A query through a partitioned table locks it
// before its partitions, so a query that waits for the locks tx holds on the partitions (an UPDATE by report_id
// alone) holds the lock DETACH would wait for: rather than deadlock with it, dropPartitions then fails with
// errLockWaiters before the DETACH. It looks for such a query before every DETACH, since one can come while an
// earlier DETACH waits for its lock. The caller retries a lock conflict (isLockConflict) only after rolling tx back,
// which releases the locks the query waits for and undoes the partitions detached before.
func dropPartitions(ctx context.Context, tx *sql.Tx, parts []Partition) error {
	// the OIDs are looked up once: a partition dropped by the loop has no name any more
	rels := make([]string, len(parts))
	for i, p := range parts {
		var oid int64
		if err := tx.QueryRowContext(ctx, `SELECT `+quote(p.QualifiedName())+`::regclass::oid`).Scan(&oid); err != nil {
			return errors.Wrapf(err, "coldstore: look up %s", p.QualifiedName())
		}
		rels[i] = strconv.FormatInt(oid, 10)
	}
	// pg_locks lists the locks of every database of the cluster, and relation OIDs are unique only within one
	waiters := `SELECT count(*) FROM pg_locks WHERE NOT granted AND locktype = 'relation'
		AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
		AND relation IN (` + strings.Join(rels, ", ") + `)`
	for _, p := range parts {
		var waiting int
		if err := tx.QueryRowContext(ctx, waiters).Scan(&waiting); err != nil {
			return errors.Wrap(err, "coldstore: look up the queries waiting for the partitions")
		}
		if waiting > 0 {
			return fmt.Errorf("%w (%d)", errLockWaiters, waiting)
		}
		if _, err := tx.ExecContext(ctx, "ALTER TABLE "+p.QualifiedParent()+" DETACH PARTITION "+p.QualifiedName()); err != nil {
			return errors.Wrapf(err, "coldstore: detach %s", p.QualifiedName())
		}
		if _, err := tx.ExecContext(ctx, "DROP TABLE "+p.QualifiedName()); err != nil {
			return errors.Wrapf(err, "coldstore: drop %s", p.QualifiedName())
		}
	}
	return nil
}
