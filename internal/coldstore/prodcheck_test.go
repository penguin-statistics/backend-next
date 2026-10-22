package coldstore

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProdCheck(t *testing.T) {
	ctx := context.Background()
	d := newTestData()
	_, pg := newTestSchema(t, d)
	dir := t.TempDir()
	opts := Options{WorkDir: t.TempDir(), Threads: 2}
	mf := NewManifest()
	for _, key := range testMonths {
		mf.PutMonth(*writeTestMonth(t, d, dir, mustMonth(t, key)))
	}

	check := func(o ProdCheckOptions, months ...string) map[string]MonthCheck {
		t.Helper()
		got := map[string]MonthCheck{}
		if err := ProdCheck(ctx, pg, mf, months, o, func(c MonthCheck) { got[c.Month] = c }); err != nil {
			t.Fatal(err)
		}
		return got
	}

	all := check(ProdCheckOptions{Reliability: true})
	if len(all) != len(testMonths) {
		t.Fatalf("checked %d months, want %d", len(all), len(testMonths))
	}
	for _, key := range testMonths {
		c := all[key]
		e := mf.Month(key)
		if c.Mismatch(true) || c.SourceRows != e.Rows || c.SourceReliableRows != e.ReliableRows {
			t.Errorf("%s = %+v, manifest rows %d, reliable %d", key, c, e.Rows, e.ReliableRows)
		}
		files, err := FileDayCounts(ctx, opts, dir, e)
		if err != nil {
			t.Fatal(err)
		}
		// the boundary reports (1970-01-01, one second before the epoch, the first and last microsecond of a
		// month) are counted on the same UTC day on both sides
		if diff := DiffDays(files, c.Days, true); len(diff) != 0 || len(files) != len(c.Days) {
			t.Errorf("%s days differ: %+v", key, diff)
		}
	}
	for _, key := range testMonths {
		want := map[string]DayCount{}
		for _, r := range d.reports {
			if !d.inMonth(mustMonth(t, key))(r) {
				continue
			}
			day := r.createdAt.UTC().Format(time.DateOnly)
			c := want[day]
			c.Rows++
			if r.stage != testRecruitStageID && r.reliability == 0 {
				c.ReliableRows++
			}
			want[day] = c
		}
		if got := all[key].Days; !reflect.DeepEqual(got, want) {
			t.Errorf("%s days = %v, want %v", key, got, want)
		}
	}

	// a report deleted after the export is found, down to its day; a reliability change only with Reliability. (The
	// writes also show that the check's read-only session did not go back to the pool.)
	var deleted testReport
	for _, r := range d.reports {
		if MonthOf(r.createdAt).Key == "2026-09" && r.id > 100 {
			deleted = r
			break
		}
	}
	if _, err := pg.ExecContext(ctx, "DELETE FROM drop_reports WHERE report_id = "+strconv.FormatInt(deleted.id, 10)); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.ExecContext(ctx, `UPDATE drop_reports SET reliability = 7 WHERE report_id = (
		SELECT min(report_id) FROM drop_reports WHERE created_at >= '2026-08-01' AND created_at < '2026-09-01'
			AND reliability = 0 AND stage_id <> 42)`); err != nil {
		t.Fatal(err)
	}

	counts := check(ProdCheckOptions{StatementTimeout: 10 * time.Second, Pause: time.Millisecond})
	for key, c := range counts {
		if c.Mismatch(false) != (key == "2026-09") {
			t.Errorf("%s = %+v", key, c)
		}
	}
	c := counts["2026-09"]
	if c.SourceRows != c.ManifestRows-1 {
		t.Errorf("2026-09 = %+v", c)
	}
	files, err := FileDayCounts(ctx, opts, dir, mf.Month("2026-09"))
	if err != nil {
		t.Fatal(err)
	}
	day := deleted.createdAt.UTC().Format(time.DateOnly)
	diff := DiffDays(files, c.Days, false)
	if len(diff) != 1 || diff[0].Day != day || diff[0].Want.Rows != diff[0].Got.Rows+1 {
		t.Errorf("days differ: %+v, want %s", diff, day)
	}

	deep := check(ProdCheckOptions{Reliability: true}, "2026-08", "2026-09")
	if len(deep) != 2 || !deep["2026-08"].Mismatch(true) || deep["2026-08"].Mismatch(false) ||
		deep["2026-08"].SourceReliableRows != deep["2026-08"].ManifestReliableRows-1 {
		t.Errorf("with reliability: %+v", deep)
	}

	// a statement that runs too long is canceled by the server
	if _, err := pg.ExecContext(ctx, `ALTER TABLE drop_reports RENAME TO drop_reports_t;
		CREATE VIEW drop_reports AS SELECT drop_reports_t.* FROM drop_reports_t, pg_sleep(0.5)`); err != nil {
		t.Fatal(err)
	}
	err = ProdCheck(ctx, pg, mf, []string{"2026-08"}, ProdCheckOptions{StatementTimeout: 100 * time.Millisecond}, func(MonthCheck) {})
	if err == nil || !strings.Contains(err.Error(), "statement timeout") {
		t.Errorf("check with a statement timeout = %v", err)
	}

	if err := ProdCheck(ctx, pg, mf, []string{"2026-10"}, ProdCheckOptions{}, func(MonthCheck) {}); err == nil ||
		!strings.Contains(err.Error(), "not in the manifest") {
		t.Errorf("check of a month not in the manifest = %v", err)
	}
}
