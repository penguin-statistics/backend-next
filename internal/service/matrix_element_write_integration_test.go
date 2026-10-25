package service

import (
	"context"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"exusiai.dev/backend-next/internal/util"
)

// elementsOfDays counts the elements of server in table per day.
func elementsOfDays(t *testing.T, db *bun.DB, table, server string) map[int]int {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT day_num, count(*) FROM `+table+` WHERE server = ? GROUP BY day_num`, server)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := make(map[int]int)
	for rows.Next() {
		var dayNum, count int
		if err := rows.Scan(&dayNum, &count); err != nil {
			t.Fatal(err)
		}
		counts[dayNum] = count
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return counts
}

// TestMatrixElementWriters checks the writers of the drop and pattern matrix elements: they write the elements of
// their day only, and, failing to insert them, return the error and leave the day with the elements before.
func TestMatrixElementWriters(t *testing.T) {
	db, dropMatrix, patternMatrix := newGlobalMatrixTestData(t)
	t.Cleanup(func() { util.Now = time.Now })
	ctx := context.Background()
	server := "CN"
	// a day in the middle and the last day the data was calculated for
	midDay := testBaseTime.Add(2*24*time.Hour + 20*time.Hour)
	lastDay := testBaseTime.Add(4*24*time.Hour + 20*time.Hour)

	// a time range starting after the last day, of its own segment, with a report (the clock is pinned before it):
	// the job on the last day calculates elements of it too, of a later day, which it must not write
	future := lastDay.Add(3 * 24 * time.Hour)
	var rangeId int64
	if err := db.NewRaw("INSERT INTO time_ranges (name, start_time, end_time, server) VALUES ('future', ?, ?, ?) RETURNING range_id",
		future, future.Add(48*time.Hour), server).Scan(ctx, &rangeId); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO drop_infos (server, stage_id, item_id, drop_type, range_id, accumulable)
		VALUES (?, 1, NULL, 'REGULAR', ?, false), (?, 1, 10, 'REGULAR', ?, false)`, server, rangeId, server, rangeId)
	mustExec(t, db, "INSERT INTO drop_pattern_elements (drop_pattern_id, item_id, quantity) VALUES (100, 10, 1)")
	mustExec(t, db, `INSERT INTO drop_reports (stage_id, pattern_id, times, created_at, reliability, server, account_id)
		VALUES (1, 100, 1, ?, 0, ?, 1)`, future.Add(time.Hour), server)
	flushCaches(t)
	util.Now = func() time.Time { return lastDay }
	if err := dropMatrix.RunCalcDropMatrixJob(ctx, server); err != nil {
		t.Fatal(err)
	}
	if err := patternMatrix.RunCalcPatternMatrixJob(ctx, server); err != nil {
		t.Fatal(err)
	}
	lastDayNum := util.GetDayNum(&lastDay, server)
	for _, table := range []string{"drop_matrix_elements", "pattern_matrix_elements"} {
		for dayNum := range elementsOfDays(t, db, table, server) {
			if dayNum > lastDayNum {
				t.Fatalf("%s: elements of %s on day %d, after the last day %d", table, server, dayNum, lastDayNum)
			}
		}
	}

	for _, drop := range []bool{true, false} {
		table := "pattern_matrix_elements"
		runJob := func() error { return patternMatrix.RunCalcPatternMatrixJob(ctx, server) }
		recalculate := func(date *time.Time) error { return patternMatrix.UpdatePatternMatrixByGivenDate(ctx, server, date) }
		if drop {
			table = "drop_matrix_elements"
			runJob = func() error { return dropMatrix.RunCalcDropMatrixJob(ctx, server) }
			recalculate = func(date *time.Time) error { return dropMatrix.UpdateDropMatrixByGivenDate(ctx, server, date) }
		}
		for _, writer := range []struct {
			name  string
			day   time.Time // whose elements the writer replaces, and fails to insert
			write func() error
		}{
			{"the job, today", lastDay, func() error {
				util.Now = func() time.Time { return lastDay }
				return runJob()
			}},
			{"the admin recalculation", midDay, func() error { return recalculate(&midDay) }},
			// the next day has no elements yet: the job replaces those of the day before too, the last day
			{"the job, yesterday at the rollover", lastDay, func() error {
				now := lastDay.Add(24 * time.Hour)
				util.Now = func() time.Time { return now }
				return runJob()
			}},
		} {
			dayNum := util.GetDayNum(&writer.day, server)
			before := elementsOfDays(t, db, table, server)
			if before[dayNum] == 0 {
				t.Fatalf("%s: no elements of %s on day %d: the data does not exercise %s", table, server, dayNum, writer.name)
			}
			// the elements of the day stay valid, new ones are refused
			mustExec(t, db, `ALTER TABLE `+table+` ADD CONSTRAINT refuse_day CHECK (day_num <> ?) NOT VALID`, dayNum)
			if err := writer.write(); err == nil {
				t.Fatalf("%s: %s succeeded although the elements of day %d cannot be inserted", table, writer.name, dayNum)
			}
			mustExec(t, db, `ALTER TABLE `+table+` DROP CONSTRAINT refuse_day`)
			after := elementsOfDays(t, db, table, server)
			for d, count := range before {
				if after[d] != count {
					t.Fatalf("%s: after %s failed, day %d has %d elements, want %d", table, writer.name, d, after[d], count)
				}
			}
		}
	}
}
