package repo

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"
	"golang.org/x/sync/errgroup"

	"exusiai.dev/backend-next/internal/model"
)

// dayElement is a drop or pattern matrix element as the ReplaceDay tests need it: the elements a replacement writes
// all carry its marker as their times, so a day shows whose elements it holds.
type dayElement struct {
	server         string
	dayNum         int
	stageId        int
	times          int
	sourceCategory string
}

type dayReplacer struct {
	table   string
	replace func(ctx context.Context, server string, dayNum int, elements []dayElement) error
}

func dayReplacers(db *bun.DB) []dayReplacer {
	dropRepo, patternRepo := NewDropMatrixElement(db), NewPatternMatrixElement(db)
	return []dayReplacer{
		{"drop_matrix_elements", func(ctx context.Context, server string, dayNum int, elements []dayElement) error {
			els := make([]*model.DropMatrixElement, len(elements))
			for i, el := range elements {
				els[i] = &model.DropMatrixElement{
					StageID: el.stageId, ItemID: 1, Quantity: 1, Times: el.times, QuantityBuckets: map[int]int{1: el.times},
					Server: el.server, SourceCategory: el.sourceCategory, DayNum: el.dayNum,
				}
			}
			return dropRepo.ReplaceDay(ctx, server, dayNum, els)
		}},
		{"pattern_matrix_elements", func(ctx context.Context, server string, dayNum int, elements []dayElement) error {
			els := make([]*model.PatternMatrixElement, len(elements))
			for i, el := range elements {
				els[i] = &model.PatternMatrixElement{
					StageID: el.stageId, PatternID: 1, Quantity: 1, Times: el.times,
					Server: el.server, SourceCategory: el.sourceCategory, DayNum: el.dayNum,
				}
			}
			return patternRepo.ReplaceDay(ctx, server, dayNum, els)
		}},
	}
}

// markedElementCount is the number of elements a replacement with marker writes.
func markedElementCount(marker int) int {
	return 1 + marker%7
}

func markedElements(server string, dayNum, marker int) []dayElement {
	elements := make([]dayElement, markedElementCount(marker))
	for i := range elements {
		elements[i] = dayElement{server: server, dayNum: dayNum, stageId: i + 1, times: marker, sourceCategory: constant.SourceCategoryAll}
	}
	return elements
}

type dayState struct{ count, markers, marker int }

// readDayState reads the elements of server on dayNum in table in a single statement, and so a single snapshot.
func readDayState(ctx context.Context, db bun.IDB, table, server string, dayNum int) (dayState, error) {
	var state dayState
	err := db.QueryRowContext(ctx, `SELECT count(*), count(DISTINCT times), coalesce(min(times), 0) FROM `+table+`
		WHERE server = ? AND day_num = ?`, server, dayNum).Scan(&state.count, &state.markers, &state.marker)
	return state, err
}

// assertDayHolds asserts that server holds on dayNum exactly the elements of the replacement with marker (none if
// marker is 0).
func assertDayHolds(t *testing.T, db *bun.DB, table, server string, dayNum, marker int) {
	t.Helper()
	state, err := readDayState(context.Background(), db, table, server, dayNum)
	if err != nil {
		t.Fatal(err)
	}
	want := dayState{}
	if marker != 0 {
		want = dayState{count: markedElementCount(marker), markers: 1, marker: marker}
	}
	if state != want {
		t.Fatalf("%s of %s on day %d: %+v, want %+v", table, server, dayNum, state, want)
	}
}

func TestReplaceDay(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	for _, r := range dayReplacers(db) {
		t.Run(r.table, func(t *testing.T) {
			for _, seed := range []struct {
				server         string
				dayNum, marker int
			}{{"CN", 100, 1}, {"CN", 101, 2}, {"US", 100, 3}} {
				if err := r.replace(ctx, seed.server, seed.dayNum, markedElements(seed.server, seed.dayNum, seed.marker)); err != nil {
					t.Fatal(err)
				}
			}
			assertDayHolds(t, db, r.table, "CN", 100, 1)
			assertDayHolds(t, db, r.table, "CN", 101, 2)
			assertDayHolds(t, db, r.table, "US", 100, 3)

			// only the day of the server is replaced
			if err := r.replace(ctx, "CN", 100, markedElements("CN", 100, 4)); err != nil {
				t.Fatal(err)
			}
			assertDayHolds(t, db, r.table, "CN", 100, 4)
			assertDayHolds(t, db, r.table, "CN", 101, 2)
			assertDayHolds(t, db, r.table, "US", 100, 3)

			// no elements empty the day
			if err := r.replace(ctx, "CN", 100, nil); err != nil {
				t.Fatal(err)
			}
			assertDayHolds(t, db, r.table, "CN", 100, 0)

			// an element of another day or server is refused, before anything is written
			for _, wrong := range []dayElement{
				{server: "CN", dayNum: 102, stageId: 9, times: 5, sourceCategory: constant.SourceCategoryAll},
				{server: "US", dayNum: 101, stageId: 9, times: 5, sourceCategory: constant.SourceCategoryAll},
			} {
				if err := r.replace(ctx, "CN", 101, append(markedElements("CN", 101, 5), wrong)); err == nil {
					t.Fatalf("replaced day 101 of CN with an element of %s on day %d", wrong.server, wrong.dayNum)
				}
				assertDayHolds(t, db, r.table, "CN", 101, 2)
			}

			// a failing insert (the source category is longer than the column) rolls the delete back
			failing := append(markedElements("CN", 101, 6), dayElement{
				server: "CN", dayNum: 101, stageId: 9, times: 6, sourceCategory: strings.Repeat("x", 40),
			})
			if err := r.replace(ctx, "CN", 101, failing); err == nil {
				t.Fatal("an insert into a too short column succeeded")
			}
			assertDayHolds(t, db, r.table, "CN", 101, 2)
		})
	}
}

// TestReplaceDayConcurrently replaces a day from several goroutines at once while another one reads it: the day
// always holds exactly the elements of one replacement, never none, never those of two.
func TestReplaceDayConcurrently(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	const writers, rounds = 4, 25
	for _, r := range dayReplacers(db) {
		t.Run(r.table, func(t *testing.T) {
			if err := r.replace(ctx, "CN", 100, markedElements("CN", 100, 1)); err != nil {
				t.Fatal(err)
			}
			// another day of the server, which no replacement may touch
			if err := r.replace(ctx, "CN", 99, markedElements("CN", 99, 3)); err != nil {
				t.Fatal(err)
			}

			var done atomic.Bool
			var reads atomic.Int64
			readers := new(errgroup.Group)
			readers.Go(func() error {
				for !done.Load() {
					state, err := readDayState(ctx, db, r.table, "CN", 100)
					if err != nil {
						return err
					}
					if state.markers != 1 || state.count != markedElementCount(state.marker) {
						t.Errorf("a reader saw %+v", state)
						return nil
					}
					reads.Add(1)
				}
				return nil
			})

			writes := new(errgroup.Group)
			for w := 0; w < writers; w++ {
				w := w
				writes.Go(func() error {
					for round := 0; round < rounds; round++ {
						marker := 2 + w*rounds + round
						if err := r.replace(ctx, "CN", 100, markedElements("CN", 100, marker)); err != nil {
							return err
						}
					}
					return nil
				})
			}
			err := writes.Wait()
			done.Store(true)
			if err != nil {
				t.Fatal(err)
			}
			if err := readers.Wait(); err != nil {
				t.Fatal(err)
			}
			if reads.Load() == 0 {
				t.Fatal("the reader read nothing")
			}

			state, err := readDayState(ctx, db, r.table, "CN", 100)
			if err != nil {
				t.Fatal(err)
			}
			if state.marker < 2 {
				t.Fatalf("no replacement left its elements: %+v", state)
			}
			assertDayHolds(t, db, r.table, "CN", 100, state.marker)
			assertDayHolds(t, db, r.table, "CN", 99, 3)
		})
	}
}
