package repo

import (
	"context"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// MatrixElementDayVersionTriggers is the number of triggers that version the days of the elements of a table: one
// per INSERT, UPDATE, DELETE and TRUNCATE (scripts/migrations/20261025_matrix_element_day_versions.sql).
const MatrixElementDayVersionTriggers = 4

// ReadMatrixElementDayVersionTriggers reads, in db, the triggers that version the days of the elements of table
// (drop_matrix_elements or pattern_matrix_elements): how many of them fire always, and what identifies them, their
// pg_trigger rows (oid, xmin, tgenabled). Disabling or enabling a trigger, dropping and creating it again, or
// another database (a restore) gives them another identity. readable reports whether the day versions can be read
// (the table exists, with SELECT granted): when they cannot, neither can a fingerprint.
func ReadMatrixElementDayVersionTriggers(ctx context.Context, db bun.IDB, table string) (enabled int, identity string, readable bool, err error) {
	err = db.NewRaw(`SELECT count(*) FILTER (WHERE t.tgenabled = 'A'),
			coalesce(string_agg(t.oid::text || ':' || t.xmin::text || ':' || t.tgenabled::text, ',' ORDER BY t.tgname), ''),
			coalesce((SELECT has_table_privilege(v, 'SELECT') FROM to_regclass('matrix_element_day_versions') AS v WHERE v IS NOT NULL), false)
		FROM pg_trigger AS t WHERE t.tgrelid = ?::regclass AND t.tgname LIKE 'matrix\_element\_day\_versions\_%'`, table).
		Scan(ctx, &enabled, &identity, &readable)
	return enabled, identity, readable, err
}

// ReadMatrixElementDayFingerprints returns, for each of bounds, a fingerprint of the day versions of the elements of
// server in tbl ('drop' or 'pattern') of the days before it, a TRUNCATE included: it changes whenever an element of
// those days is written, in the transaction of the write. The fingerprints are of the snapshot of db. Not the
// greatest version: transactions commit out of the order of their versions.
func ReadMatrixElementDayFingerprints(ctx context.Context, db bun.IDB, tbl, server string, bounds []int) (map[int]string, error) {
	var rows []struct {
		Bound       int    `bun:"bound"`
		Fingerprint string `bun:"fingerprint"`
	}
	err := db.NewRaw(`SELECT b.bound, count(v.version)::text || ':' ||
			coalesce(md5(string_agg(v.server || ':' || v.day_num::text || ':' || v.version::text, ',' ORDER BY v.server, v.day_num)), '') AS fingerprint
		FROM unnest(?::int[]) AS b(bound)
		LEFT JOIN matrix_element_day_versions AS v ON v.tbl = ? AND v.server IN (?, '*') AND v.day_num < b.bound
		GROUP BY b.bound`, pgdialect.Array(bounds), tbl, server).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	fingerprints := make(map[int]string, len(rows))
	for _, row := range rows {
		fingerprints[row.Bound] = row.Fingerprint
	}
	return fingerprints, nil
}
