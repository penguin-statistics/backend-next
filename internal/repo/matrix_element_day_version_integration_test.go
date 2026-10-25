package repo

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
)

// The fingerprints of the day versions change with a write of a day before their bound only, of their server (or a
// TRUNCATE) and table, and the triggers read as a reader of the fingerprints reads them.
func TestReadMatrixElementDayFingerprints(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	bounds := []int{0, 6, 11, 20}
	read := func() map[int]string {
		t.Helper()
		fingerprints, err := ReadMatrixElementDayFingerprints(ctx, db, "drop", "CN", bounds)
		if err != nil {
			t.Fatal(err)
		}
		if len(fingerprints) != len(bounds) {
			t.Fatalf("%d fingerprints for %d bounds", len(fingerprints), len(bounds))
		}
		return fingerprints
	}
	// step runs query, then requires the fingerprints of exactly the bounds in changed to have changed
	fingerprints := read()
	step := func(what, query string, changed ...int) {
		t.Helper()
		mustExec(t, db, query)
		after := read()
		for _, bound := range bounds {
			want := false
			for _, c := range changed {
				want = want || c == bound
			}
			if got := after[bound] != fingerprints[bound]; got != want {
				t.Fatalf("%s: the fingerprint of the days before %d changed: %v, want %v (%q, then %q)", what, bound, got, want, fingerprints[bound], after[bound])
			}
		}
		fingerprints = after
	}
	insert := func(server string, dayNum int) string {
		return fmt.Sprintf("INSERT INTO drop_matrix_elements (stage_id, item_id, quantity, times, server, day_num) VALUES (1, 1, 1, 1, '%s', %d)", server, dayNum)
	}
	step("day 5", insert("CN", 5), 6, 11, 20)
	step("day 10", insert("CN", 10), 11, 20)
	step("day 12", insert("CN", 12), 20)
	step("day 5 again", "UPDATE drop_matrix_elements SET times = 2 WHERE day_num = 5", 6, 11, 20)
	step("another server", insert("US", 5))
	step("the pattern elements", "INSERT INTO pattern_matrix_elements (stage_id, pattern_id, quantity, times, server, day_num) VALUES (1, 1, 1, 1, 'CN', 5)")
	step("a TRUNCATE of the pattern elements", "TRUNCATE pattern_matrix_elements")
	step("a delete of nothing", "DELETE FROM drop_matrix_elements WHERE day_num = 99")
	step("a TRUNCATE", "TRUNCATE drop_matrix_elements", 0, 6, 11, 20)

	for _, table := range []string{"drop_matrix_elements", "pattern_matrix_elements"} {
		enabled, identity, readable, err := ReadMatrixElementDayVersionTriggers(ctx, db, table)
		if err != nil {
			t.Fatal(err)
		}
		if enabled != MatrixElementDayVersionTriggers || identity == "" || !readable {
			t.Fatalf("%s: %d triggers fire always, identity %q, readable %v; want %d, an identity, readable", table, enabled, identity, readable, MatrixElementDayVersionTriggers)
		}
	}

	// a role that may not read the versions
	role := fmt.Sprintf("it_reader_%d", rand.Int63())
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
	mustExec(t, db, fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", schema, role))
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(ctx, "DISCARD ALL")
	if _, err := conn.ExecContext(ctx, "SET ROLE "+role); err != nil {
		t.Fatal(err)
	}
	enabled, _, readable, err := ReadMatrixElementDayVersionTriggers(ctx, conn, "drop_matrix_elements")
	if err != nil {
		t.Fatal(err)
	}
	if enabled != MatrixElementDayVersionTriggers || readable {
		t.Fatalf("without SELECT on the versions: %d triggers fire always, readable %v; want %d, not readable", enabled, readable, MatrixElementDayVersionTriggers)
	}
}
