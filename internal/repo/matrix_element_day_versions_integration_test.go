package repo

// The day versions of the matrix elements (scripts/migrations/20261025_matrix_element_day_versions.sql, and the same
// objects in scripts/newdb): which writes bump which days, for which roles and settings, under concurrency, and how
// changes to the triggers show to a reader.

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/driver/pgdriver"
	"golang.org/x/sync/errgroup"
)

const dayVersionsDownMigration = "../../scripts/migrations/20261025_matrix_element_day_versions_down.sql"

// dayVersionTables are the element tables with day versions, by the tbl of their versions.
var dayVersionTables = []struct{ tbl, table, columns, values string }{
	// the values fit the rows newDBSchema creates for the foreign keys
	{"drop", "drop_matrix_elements", "stage_id, item_id, quantity, times, server, day_num", "1, 11, 1, 1"},
	{"pattern", "pattern_matrix_elements", "stage_id, pattern_id, quantity, times, server, day_num", "1, 1, 1, 1"},
}

// readDayVersions reads every day version as "tbl server day_num" -> version.
func readDayVersions(t *testing.T, db bun.IDB) map[string]int64 {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "SELECT tbl, server, day_num, version FROM matrix_element_day_versions")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	versions := make(map[string]int64)
	for rows.Next() {
		var tbl, server string
		var dayNum int
		var version int64
		if err := rows.Scan(&tbl, &server, &dayNum, &version); err != nil {
			t.Fatal(err)
		}
		versions[fmt.Sprintf("%s %s %d", tbl, server, dayNum)] = version
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return versions
}

// bumpedDays returns the days whose version changed from before to after, sorted. Versions are never removed, and a
// new version is above every version before.
func bumpedDays(t *testing.T, before, after map[string]int64) []string {
	t.Helper()
	var maxBefore int64
	for _, v := range before {
		maxBefore = max(maxBefore, v)
	}
	bumped := []string{}
	for day, v := range after {
		if old, ok := before[day]; ok && old == v {
			continue
		}
		if v <= maxBefore {
			t.Errorf("day %s got version %d, not above the versions before (up to %d)", day, v, maxBefore)
		}
		bumped = append(bumped, day)
	}
	for day := range before {
		if _, ok := after[day]; !ok {
			t.Errorf("the version of day %s was removed", day)
		}
	}
	sort.Strings(bumped)
	return bumped
}

// expectBumps runs write and requires it to bump exactly the days want (as "server day_num" of tbl).
func expectBumps(t *testing.T, db bun.IDB, tbl string, want []string, what string, write func() error) {
	t.Helper()
	before := readDayVersions(t, db)
	if err := write(); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	wantDays := []string{}
	for _, day := range want {
		wantDays = append(wantDays, tbl+" "+day)
	}
	sort.Strings(wantDays)
	if got := bumpedDays(t, before, readDayVersions(t, db)); !reflect.DeepEqual(got, wantDays) {
		t.Fatalf("%s bumped %v, want %v", what, got, wantDays)
	}
}

func execAll(ctx context.Context, db bun.IDB, queries ...string) error {
	for _, query := range queries {
		if _, err := db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("%s: %w", query, err)
		}
	}
	return nil
}

// TestMatrixElementDayVersionsBumps runs every kind of write on both element tables, with the objects of the
// migration (on the tables of the tests) and of scripts/newdb, and requires each to bump exactly the days it touched.
func TestMatrixElementDayVersionsBumps(t *testing.T) {
	for _, schema := range []struct {
		name string
		ddl  func(t *testing.T) string
	}{
		{"migration", func(t *testing.T) string { return testSchema + readFile(t, dayVersionsMigration) }},
		{"newdb", newDBSchema},
	} {
		t.Run(schema.name, func(t *testing.T) {
			db := newTestDBWithSchema(t, schema.ddl(t))
			ctx := context.Background()
			var serverVersion int
			if err := db.NewRaw("SELECT current_setting('server_version_num')::int").Scan(ctx, &serverVersion); err != nil {
				t.Fatal(err)
			}
			for _, tt := range dayVersionTables {
				t.Run(tt.table, func(t *testing.T) {
					testDayVersionBumps(t, db, serverVersion, tt.tbl, tt.table, tt.columns, tt.values)
				})
			}
		})
	}
}

func testDayVersionBumps(t *testing.T, db *bun.DB, serverVersion int, tbl, table, columns, values string) {
	ctx := context.Background()
	insert := func(days ...string) string {
		rows := make([]string, len(days))
		for i, day := range days {
			var server string
			var dayNum int
			fmt.Sscanf(day, "%s %d", &server, &dayNum)
			rows[i] = fmt.Sprintf("(%s, '%s', %d)", values, server, dayNum)
		}
		return fmt.Sprintf("INSERT INTO %s (%s) VALUES %s", table, columns, strings.Join(rows, ", "))
	}
	exec := func(queries ...string) func() error {
		return func() error { return execAll(ctx, db, queries...) }
	}
	elementId := func(server string, dayNum int) int64 {
		t.Helper()
		var id int64
		if err := db.NewRaw(fmt.Sprintf("SELECT min(element_id) FROM %s WHERE server = ? AND day_num = ?", table), server, dayNum).
			Scan(ctx, &id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	// two elements of one day, and the days of the elements of the other table untouched
	expectBumps(t, db, tbl, []string{"CN 100", "CN 101", "US 100"}, "INSERT",
		exec(insert("CN 100", "US 100", "CN 101", "CN 100")))
	expectBumps(t, db, tbl, []string{"CN 101"}, "UPDATE",
		exec(fmt.Sprintf("UPDATE %s SET times = times + 1 WHERE server = 'CN' AND day_num = 101", table)))
	expectBumps(t, db, tbl, []string{"US 100", "US 102"}, "UPDATE of day_num",
		exec(fmt.Sprintf("UPDATE %s SET day_num = 102 WHERE server = 'US' AND day_num = 100", table)))
	expectBumps(t, db, tbl, nil, "UPDATE of no row", exec(fmt.Sprintf("UPDATE %s SET times = 0 WHERE false", table)))
	expectBumps(t, db, tbl, []string{"CN 100"}, "DELETE",
		exec(fmt.Sprintf("DELETE FROM %s WHERE server = 'CN' AND day_num = 100", table)))
	expectBumps(t, db, tbl, nil, "DELETE of no row", exec(fmt.Sprintf("DELETE FROM %s WHERE false", table)))

	if serverVersion >= 150000 {
		// an update, a delete and an insert in one statement
		merge := fmt.Sprintf(`MERGE INTO %s AS e USING (VALUES (%d, 'update'), (%d, 'delete'), (0, 'insert')) AS s(id, action)
			ON e.element_id = s.id
			WHEN MATCHED AND s.action = 'delete' THEN DELETE
			WHEN MATCHED THEN UPDATE SET times = e.times + 1
			WHEN NOT MATCHED THEN INSERT (%s) VALUES (%s, 'KR', 5)`,
			table, elementId("CN", 101), elementId("US", 102), columns, values)
		expectBumps(t, db, tbl, []string{"CN 101", "KR 5", "US 102"}, "MERGE", exec(merge))
	} else {
		t.Logf("MERGE not tested: PostgreSQL %d has none", serverVersion)
	}

	expectBumps(t, db, tbl, []string{"JP 7", "JP 8"}, "COPY", func() error {
		conn, err := db.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		data := strings.ReplaceAll(values, ", ", "\t")
		_, err = pgdriver.CopyFrom(ctx, conn, strings.NewReader(data+"\tJP\t7\n"+data+"\tJP\t8\n"+data+"\tJP\t7\n"),
			fmt.Sprintf("COPY %s (%s) FROM STDIN", table, columns))
		return err
	})

	// nothing of a transaction rolled back
	expectBumps(t, db, tbl, nil, "a rolled back transaction", func() error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := execAll(ctx, tx, insert("CN 200"), fmt.Sprintf("UPDATE %s SET day_num = day_num + 1", table),
			fmt.Sprintf("DELETE FROM %s", table), "TRUNCATE "+table); err != nil {
			return err
		}
		return tx.Rollback()
	})

	// ENABLE ALWAYS: as for logical replication or pg_restore --disable-triggers of the data
	expectBumps(t, db, tbl, []string{"CN 300"}, "INSERT with session_replication_role = replica", func() error {
		conn, err := db.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		defer conn.ExecContext(ctx, "RESET session_replication_role")
		return execAll(ctx, conn, "SET session_replication_role = replica", insert("CN 300"))
	})

	// below every day of every server, every time
	expectBumps(t, db, tbl, []string{"* -1"}, "TRUNCATE", exec("TRUNCATE "+table))
	expectBumps(t, db, tbl, []string{"* -1"}, "TRUNCATE again", exec("TRUNCATE "+table))
}

// TestMatrixElementDayVersionsUnprivilegedWriter writes elements as a role with no privilege on the versions: the
// triggers write them all the same (SECURITY DEFINER), also when the role has a temporary table and sequence of the
// same names.
func TestMatrixElementDayVersionsUnprivilegedWriter(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	role := fmt.Sprintf("it_writer_%d", rand.Int63())
	var schema string
	if err := db.NewRaw("SELECT current_schema()").Scan(ctx, &schema); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, "CREATE ROLE "+role+" NOLOGIN")
	t.Cleanup(func() {
		if err := execAll(context.Background(), db, "DROP OWNED BY "+role, "DROP ROLE "+role); err != nil {
			t.Error(err)
		}
	})
	mustExec(t, db, fmt.Sprintf(`GRANT USAGE ON SCHEMA %[1]s TO %[2]s;
		GRANT SELECT, INSERT, UPDATE, DELETE ON drop_matrix_elements, pattern_matrix_elements TO %[2]s;
		GRANT USAGE ON SEQUENCE drop_matrix_elements_element_id_seq, pattern_matrix_elements_element_id_seq TO %[2]s`,
		schema, role))

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// the role and the temporary objects do not go back to the pool with the connection
	defer conn.ExecContext(ctx, "DISCARD ALL")
	mustConn := func(query string) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, query); err != nil {
			t.Fatalf("as %s: %s: %v", role, query, err)
		}
	}
	mustConn("SET ROLE " + role)

	var privileges string
	if err := conn.NewRaw(`SELECT has_table_privilege('matrix_element_day_versions', 'SELECT, INSERT, UPDATE') || ' ' ||
		has_sequence_privilege('matrix_element_day_version_seq', 'USAGE, UPDATE') || ' ' ||
		has_function_privilege('bump_matrix_element_day_versions()', 'EXECUTE')`).Scan(ctx, &privileges); err != nil {
		t.Fatal(err)
	}
	if privileges != "false false false" {
		t.Fatalf("the role has privileges %q on the version table, its sequence and the function, want none", privileges)
	}
	_, err = conn.ExecContext(ctx, "INSERT INTO matrix_element_day_versions VALUES ('drop', 'CN', 1, 1)")
	var pgErr pgdriver.Error
	if !errors.As(err, &pgErr) || pgErr.Field('C') != "42501" {
		t.Fatalf("an insert into the version table by the role: %v, want insufficient_privilege (42501)", err)
	}
	// would come first in the search path of the function if it were not after its schema
	mustConn("CREATE TEMPORARY TABLE matrix_element_day_versions (tbl text, server text, day_num integer, version bigint, PRIMARY KEY (tbl, server, day_num))")
	mustConn("CREATE TEMPORARY SEQUENCE matrix_element_day_version_seq")

	before := readDayVersions(t, db)
	for _, query := range []string{
		"INSERT INTO drop_matrix_elements (stage_id, item_id, quantity, times, server, day_num) VALUES (1, 1, 1, 1, 'CN', 10), (1, 1, 1, 1, 'US', 11)",
		"UPDATE drop_matrix_elements SET day_num = 12 WHERE server = 'US'",
		"DELETE FROM drop_matrix_elements WHERE server = 'CN'",
		"INSERT INTO pattern_matrix_elements (stage_id, pattern_id, quantity, times, server, day_num) VALUES (1, 1, 1, 1, 'JP', 20)",
	} {
		mustConn(query)
	}
	got := bumpedDays(t, before, readDayVersions(t, db))
	if want := []string{"drop CN 10", "drop US 11", "drop US 12", "pattern JP 20"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the writes of the role bumped %v, want %v", got, want)
	}
	var temporary int
	if err := conn.NewRaw("SELECT count(*) FROM pg_temp.matrix_element_day_versions").Scan(ctx, &temporary); err != nil {
		t.Fatal(err)
	}
	if temporary != 0 {
		t.Fatalf("the triggers wrote %d versions into the temporary table of the role", temporary)
	}
}

// TestMatrixElementDayVersionsConcurrentWriters writes the same days from several goroutines at once in the ways
// the writers do (deleting then inserting a day in one transaction, with and without ReplaceDay's lock) and with
// statements over several days in any order: no deadlock, and every day written has a version. (Concurrent UPDATEs
// of the same rows can deadlock on the rows themselves, with or without the triggers: not tested here.)
func TestMatrixElementDayVersionsConcurrentWriters(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	const writers, rounds = 8, 20
	days := []int{1, 2, 3, 4}
	// the days in a random order
	shuffled := func(r *rand.Rand, format string) []string {
		out := []string{}
		for _, i := range r.Perm(len(days)) {
			out = append(out, fmt.Sprintf(format, days[i]))
		}
		return out
	}
	replace := dayReplacers(db)[0].replace

	before := readDayVersions(t, db)
	g := new(errgroup.Group)
	for w := 0; w < writers; w++ {
		r := rand.New(rand.NewSource(int64(w)))
		g.Go(func() error {
			for round := 0; round < rounds; round++ {
				day := days[r.Intn(len(days))]
				var err error
				switch r.Intn(4) {
				case 0: // a day replaced as ReplaceDay does it, without its lock
					err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
						return execAll(ctx, tx,
							fmt.Sprintf("DELETE FROM drop_matrix_elements WHERE server = 'CN' AND day_num = %d", day),
							fmt.Sprintf("INSERT INTO drop_matrix_elements (stage_id, item_id, quantity, times, server, day_num) VALUES (1, 1, 1, 1, 'CN', %d)", day))
					})
				case 1:
					err = replace(ctx, "CN", day, markedElements("CN", day, r.Intn(9)+1))
				case 2:
					_, err = db.ExecContext(ctx, "INSERT INTO drop_matrix_elements (stage_id, item_id, quantity, times, server, day_num) VALUES "+
						strings.Join(shuffled(r, "(1, 1, 1, 1, 'CN', %d)"), ", "))
				case 3:
					_, err = db.ExecContext(ctx, "DELETE FROM drop_matrix_elements WHERE server = 'CN' AND day_num IN ("+
						strings.Join(shuffled(r, "%d"), ", ")+")")
				}
				if err != nil {
					return err
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}
	got := bumpedDays(t, before, readDayVersions(t, db))
	if want := []string{"drop CN 1", "drop CN 2", "drop CN 3", "drop CN 4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bumped %v, want %v", got, want)
	}
}

// TestMatrixElementDayVersionsLaterCommitOfSmallerVersion has the transaction with the smaller version of a day
// commit last: the version of the day goes down, and so does the greatest version, but the versions of the days
// still change, so a reader compares them all.
func TestMatrixElementDayVersionsLaterCommitOfSmallerVersion(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	write := func(db bun.IDB, query string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	insert := "INSERT INTO drop_matrix_elements (stage_id, item_id, quantity, times, server, day_num) VALUES (1, 1, 1, 1, 'CN', 50)"
	write(db, "INSERT INTO drop_matrix_elements (stage_id, item_id, quantity, times, server, day_num) VALUES (1, 1, 1, 1, 'CN', 49)")

	txA, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer txA.Rollback()
	write(txA, insert)
	txB, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer txB.Rollback()
	var pidB int
	if err := txB.NewRaw("SELECT pg_backend_pid()").Scan(ctx, &pidB); err != nil {
		t.Fatal(err)
	}
	// B takes its version, then waits for A's lock on the version of the day
	writtenB := make(chan error, 1)
	go func() {
		_, err := txB.ExecContext(ctx, insert)
		writtenB <- err
	}()
	for deadline := time.Now().Add(10 * time.Second); ; {
		var waiting bool
		if err := db.NewRaw("SELECT EXISTS (SELECT FROM pg_locks WHERE pid = ? AND NOT granted)", pidB).Scan(ctx, &waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the second writer does not wait for the first")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A takes a greater version than B's, and commits first
	write(txA, "UPDATE drop_matrix_elements SET times = 2 WHERE server = 'CN' AND day_num = 50")
	if err := txA.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-writtenB; err != nil {
		t.Fatal(err)
	}
	afterA := readDayVersions(t, db)
	if err := txB.Commit(); err != nil {
		t.Fatal(err)
	}
	afterB := readDayVersions(t, db)

	greatest := func(versions map[string]int64) (v int64) {
		for _, version := range versions {
			v = max(v, version)
		}
		return v
	}
	if !(afterB["drop CN 50"] < afterA["drop CN 50"]) || !(greatest(afterB) < greatest(afterA)) {
		t.Fatalf("the versions after the commit of A %v, then of B %v: B's are not smaller", afterA, afterB)
	}
	if afterB["drop CN 49"] != afterA["drop CN 49"] {
		t.Fatalf("day 49 changed from %d to %d", afterA["drop CN 49"], afterB["drop CN 49"])
	}
}

// triggerIdentity reads the triggers of the day versions of table as a reader does: how many fire always, and
// what identifies their pg_trigger rows.
func triggerIdentity(t *testing.T, db bun.IDB, table string) (enabled int, identity string) {
	t.Helper()
	if err := db.NewRaw(`SELECT count(*) FILTER (WHERE t.tgenabled = 'A'),
			coalesce(string_agg(t.oid::text || ':' || t.xmin::text || ':' || t.tgenabled::text, ',' ORDER BY t.tgname), '')
		FROM pg_trigger AS t WHERE t.tgrelid = ?::regclass AND t.tgname LIKE 'matrix\_element\_day\_versions\_%'`, table).
		Scan(context.Background(), &enabled, &identity); err != nil {
		t.Fatal(err)
	}
	return enabled, identity
}

// TestMatrixElementDayVersionTriggerIdentity disables and enables the triggers, as an operator or pg_restore
// --disable-triggers does, and drops and creates them again (the down migration, then the migration): every change
// gives the triggers another identity, and leaves them firing always only once they are enabled always again.
func TestMatrixElementDayVersionTriggerIdentity(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	for _, tt := range dayVersionTables {
		t.Run(tt.table, func(t *testing.T) {
			seen := map[string]string{}
			step := func(what string, wantEnabled int, queries ...string) {
				t.Helper()
				if err := execAll(ctx, db, queries...); err != nil {
					t.Fatal(err)
				}
				enabled, identity := triggerIdentity(t, db, tt.table)
				if enabled != wantEnabled {
					t.Fatalf("%s: %d triggers fire always, want %d", what, enabled, wantEnabled)
				}
				if earlier, ok := seen[identity]; ok {
					t.Fatalf("%s: the identity of the triggers is that %s: %s", what, earlier, identity)
				}
				seen[identity] = what
			}
			alter := func(action string) string { return fmt.Sprintf("ALTER TABLE %s %s", tt.table, action) }
			enableAlways := alter(`ENABLE ALWAYS TRIGGER matrix_element_day_versions_ins,
				ENABLE ALWAYS TRIGGER matrix_element_day_versions_del, ENABLE ALWAYS TRIGGER matrix_element_day_versions_upd,
				ENABLE ALWAYS TRIGGER matrix_element_day_versions_trunc`)
			update := fmt.Sprintf("UPDATE %s SET times = times + 1", tt.table)
			mustExec(t, db, fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s, 'CN', 1)", tt.table, tt.columns, tt.values))

			step("after the migration", 4)
			step("with the update trigger disabled", 3, alter("DISABLE TRIGGER matrix_element_day_versions_upd"))
			// the writes a reader must not miss
			expectBumps(t, db, tt.tbl, nil, "UPDATE with the trigger disabled", func() error { return execAll(ctx, db, update) })
			step("with the update trigger enabled", 3, alter("ENABLE TRIGGER matrix_element_day_versions_upd"))
			step("with the update trigger enabled always", 4, enableAlways)
			step("after pg_restore --disable-triggers", 0, alter("DISABLE TRIGGER ALL"), alter("ENABLE TRIGGER ALL"))
			step("enabled always again", 4, enableAlways)
			// the version table is gone with the triggers; the elements can be written all the same
			step("after the down migration", 0, readFile(t, dayVersionsDownMigration), update)
			var versionTable *string
			if err := db.NewRaw("SELECT to_regclass('matrix_element_day_versions')::text").Scan(ctx, &versionTable); err != nil {
				t.Fatal(err)
			}
			if versionTable != nil {
				t.Fatalf("the down migration left %s", *versionTable)
			}
			step("after the migration again", 4, readFile(t, dayVersionsMigration))
			expectBumps(t, db, tt.tbl, []string{"CN 1"}, "UPDATE after the migration again", func() error { return execAll(ctx, db, update) })
		})
	}
}

// TestMatrixElementDayVersionsNewDBMatchesMigration requires the objects of scripts/newdb to be those of the
// migration: the version table, its sequence, the function and the triggers.
func TestMatrixElementDayVersionsNewDBMatchesMigration(t *testing.T) {
	definitions := func(db *bun.DB) []string {
		t.Helper()
		ctx := context.Background()
		var schema string
		if err := db.NewRaw("SELECT current_schema()").Scan(ctx, &schema); err != nil {
			t.Fatal(err)
		}
		var defs []string
		for _, query := range []string{
			`SELECT attname || ' ' || format_type(atttypid, atttypmod) || ' ' || attnotnull FROM pg_attribute
				WHERE attrelid = 'matrix_element_day_versions'::regclass AND attnum > 0 AND NOT attisdropped ORDER BY attnum`,
			`SELECT conname || ' ' || pg_get_constraintdef(oid) FROM pg_constraint
				WHERE conrelid = 'matrix_element_day_versions'::regclass ORDER BY conname`,
			`SELECT seqtypid::regtype || ' ' || seqstart || ' ' || seqincrement || ' ' || seqmin || ' ' || seqmax || ' ' || seqcycle
				FROM pg_sequence WHERE seqrelid = 'matrix_element_day_version_seq'::regclass`,
			`SELECT pg_get_functiondef(p.oid) || ' ' || coalesce(p.proacl::text, '') FROM pg_proc AS p
				WHERE p.oid = 'bump_matrix_element_day_versions()'::regprocedure`,
			`SELECT tgname || ' ' || tgenabled::text || ' ' || pg_get_triggerdef(oid) FROM pg_trigger
				WHERE tgrelid IN ('drop_matrix_elements'::regclass, 'pattern_matrix_elements'::regclass)
				AND tgname LIKE 'matrix\_element\_day\_versions\_%' ORDER BY tgrelid::regclass::text, tgname`,
		} {
			var rows []string
			if err := db.NewRaw(query).Scan(ctx, &rows); err != nil {
				t.Fatal(err)
			}
			if len(rows) == 0 {
				t.Fatalf("nothing from %s", query)
			}
			for _, row := range rows {
				defs = append(defs, strings.ReplaceAll(row, schema, "<schema>"))
			}
		}
		return defs
	}
	migration := definitions(newTestDB(t))
	newDB := definitions(newTestDBWithSchema(t, newDBSchema(t)))
	if !reflect.DeepEqual(migration, newDB) {
		t.Fatalf("the objects of scripts/newdb differ from those of the migration:\nnewdb     %q\nmigration %q", newDB, migration)
	}
}
