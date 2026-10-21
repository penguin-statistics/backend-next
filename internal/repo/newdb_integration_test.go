package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
)

// newDBSchema is the schema of the new database (scripts/newdb), with the partitions of 2026-09 and 2026-10 and the
// rows the foreign keys of drop_reports and drop_pattern_elements need for the reports of seedDropReports.
func newDBSchema(t *testing.T) string {
	t.Helper()
	ddl := ""
	for _, file := range []string{"schema.sql", "indexes.sql"} {
		b, err := os.ReadFile("../../scripts/newdb/" + file)
		if err != nil {
			t.Fatal(err)
		}
		ddl += string(b) + "\n"
	}
	return ddl + `
SELECT create_report_partitions('2026-09-01'), create_report_partitions('2026-10-15');
INSERT INTO zones (zone_id, ark_zone_id, index, category, name, existence) VALUES (1, 'main_0', 0, 'MAINLINE', '{}', '{}');
INSERT INTO stages (stage_id, ark_stage_id, zone_id, code, existence)
	SELECT i, 'stage_' || i, 1, '{}', '{}' FROM generate_series(1, 5) i;
INSERT INTO items (item_id, ark_item_id, existence, name, sort_id, rarity, keywords)
	SELECT i, 'item_' || i, '{}', '{}', i, 1, '{}' FROM generate_series(11, 14) i;
INSERT INTO accounts (account_id, penguin_id, weight, created_at)
	SELECT i, 'p' || i, 1, now() FROM generate_series(1, 3) i;
INSERT INTO drop_patterns (pattern_id, hash) SELECT i, 'h' || i FROM generate_series(1, 20) i;
`
}

// TestDropReportReadsOnNewDBSchema runs the reads of drop_reports on the partitioned schema of the new database and
// on today's schema, with the same reports across the bound of two monthly partitions, and requires the same results.
func TestDropReportReadsOnNewDBSchema(t *testing.T) {
	oldDB := newTestDB(t)
	newDB := newTestDBWithSchema(t, newDBSchema(t))
	oldRepo, newRepo := NewDropReport(oldDB), NewDropReport(newDB)
	ctx := context.Background()

	// 24 hours around the start of 2026-10 (UTC)
	base := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
	f := seedDropReportsFrom(t, rand.New(rand.NewSource(3)), oldDB, base)
	seedDropReportsFrom(t, rand.New(rand.NewSource(3)), newDB, base)

	var partitions []string
	if err := newDB.NewSelect().TableExpr("drop_reports").ColumnExpr("DISTINCT tableoid::regclass::text").Scan(ctx, &partitions); err != nil {
		t.Fatal(err)
	}
	sort.Strings(partitions)
	if !reflect.DeepEqual(partitions, []string{"drop_reports_2026_09", "drop_reports_2026_10"}) {
		t.Fatalf("reports are in partitions %v", partitions)
	}

	// same runs a read on both databases and compares the results as multisets
	same := func(what string, read func(r *DropReport) (interface{}, error)) {
		t.Helper()
		want, err := read(oldRepo)
		if err != nil {
			t.Fatalf("%s on today's schema: %v", what, err)
		}
		got, err := read(newRepo)
		if err != nil {
			t.Fatalf("%s on the new schema: %v", what, err)
		}
		if w, g := canonicalRows(t, want), canonicalRows(t, got); !reflect.DeepEqual(w, g) {
			t.Fatalf("%s: got %d rows on the new schema, %d on today's\ngot  %v\nwant %v", what, len(g), len(w), g, w)
		}
	}

	r := rand.New(rand.NewSource(4))
	for round := 0; round < 12; round++ {
		accountId := null.Int{}
		if round%3 == 2 {
			accountId = null.IntFrom(f.accountIds[r.Intn(len(f.accountIds))])
		}
		server := []string{"CN", "US"}[round%2]
		queryCtxs := randomIntervals(r, f, server, accountId, round%4 == 0)
		name := func(what string) string { return fmt.Sprintf("round %d: %s", round, what) }

		same(name("CalcDropMatrixForIntervalsBySourceCategories"), func(repo *DropReport) (interface{}, error) {
			times, quantities, err := repo.CalcDropMatrixForIntervalsBySourceCategories(ctx, queryCtxs, allSourceCategories)
			return []interface{}{times, quantities}, err
		})
		for _, queryCtx := range queryCtxs {
			single := *queryCtx
			single.SourceCategory = allSourceCategories[r.Intn(len(allSourceCategories))]
			same(name("CalcTotalTimes"), func(repo *DropReport) (interface{}, error) { return repo.CalcTotalTimes(ctx, &single) })
			same(name("CalcQuantityUniqCount"), func(repo *DropReport) (interface{}, error) { return repo.CalcQuantityUniqCount(ctx, &single) })
			same(name("CalcTotalQuantityForPatternMatrix"), func(repo *DropReport) (interface{}, error) {
				return repo.CalcTotalQuantityForPatternMatrix(ctx, &single)
			})
			same(name("CalcTotalQuantityForPatternMatrixBySourceCategories"), func(repo *DropReport) (interface{}, error) {
				return repo.CalcTotalQuantityForPatternMatrixBySourceCategories(ctx, &single, allSourceCategories)
			})
			if single.StageItemFilter != nil { // as for advanced queries, which always have one
				same(name("GetDropReports"), func(repo *DropReport) (interface{}, error) { return repo.GetDropReports(ctx, &single) })
			}
		}
		withItems := *queryCtxs[0]
		withItems.StageItemFilter = &map[int][]int{f.stageIds[0]: {11, 12}, f.stageIds[1]: {13}}
		same(name("GetDropReports with items"), func(repo *DropReport) (interface{}, error) { return repo.GetDropReports(ctx, &withItems) })

		sourceCategory := allSourceCategories[round%len(allSourceCategories)]
		same(name("CalcTotalQuantityForTrend"), func(repo *DropReport) (interface{}, error) {
			return repo.CalcTotalQuantityForTrend(ctx, server, &base, 6*time.Hour, 4, map[int][]int{1: {11, 12}, 2: {}, 3: {13, 14}}, accountId, sourceCategory)
		})
		same(name("CalcTotalTimesForTrend"), func(repo *DropReport) (interface{}, error) {
			return repo.CalcTotalTimesForTrend(ctx, server, &base, 6*time.Hour, 4, f.stageIds, accountId, sourceCategory)
		})
	}

	// under a statement timeout (a transaction of its own)
	timed := randomIntervals(r, f, "CN", null.Int{}, false)[0]
	same("CalcTotalTimes with a statement timeout", func(repo *DropReport) (interface{}, error) {
		var results []*model.TotalTimesResult
		err := repo.RunWithStatementTimeout(ctx, time.Minute, func(ctx context.Context) error {
			var err error
			results, err = repo.CalcTotalTimes(ctx, timed)
			return err
		})
		return results, err
	})

	// DeleteDropReport marks a report of each partition unreliable by its id, but only one submitted within
	// RecallMaxAge (the recall's bound): an older fixture report is left alone on both schemas
	for _, at := range []time.Time{base, base.Add(20 * time.Hour)} {
		for _, db := range []*bun.DB{oldDB, newDB} {
			var reportId int
			if err := db.NewSelect().TableExpr("drop_reports").Column("report_id").
				Where("created_at >= ? AND reliability = 0", at).Order("created_at", "report_id").Limit(1).Scan(ctx, &reportId); err != nil {
				t.Fatal(err)
			}
			rowsAffected, err := NewDropReport(db).DeleteDropReport(ctx, reportId)
			if err != nil {
				t.Fatal(err)
			}
			want := int64(0)
			if time.Since(at) < RecallMaxAge {
				want = 1
			}
			if rowsAffected != want {
				t.Errorf("DeleteDropReport(%d) affected %d rows, want %d", reportId, rowsAffected, want)
			}
		}
	}
	all := &model.DropReportQueryContext{Server: "CN", StartTime: &f.boundaries[0], EndTime: &f.boundaries[len(f.boundaries)-1], SourceCategory: constant.SourceCategoryAll}
	same("CalcTotalTimes after DeleteDropReport", func(repo *DropReport) (interface{}, error) { return repo.CalcTotalTimes(ctx, all) })
}

// canonicalRows flattens results (slices, maps of slices, nested) into the sorted JSON of their rows.
func canonicalRows(t *testing.T, v interface{}) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var tree interface{}
	if err := json.Unmarshal(b, &tree); err != nil {
		t.Fatal(err)
	}
	rows := make([]string, 0)
	var walk func(prefix string, v interface{})
	walk = func(prefix string, v interface{}) {
		switch v := v.(type) {
		case []interface{}:
			for i, e := range v {
				// the elements of a slice of slices or maps are told apart by their position (e.g. intervals)
				if _, nested := e.([]interface{}); nested {
					walk(fmt.Sprintf("%s[%d]", prefix, i), e)
					continue
				}
				if m, ok := e.(map[string]interface{}); ok && isGrouping(m) {
					walk(fmt.Sprintf("%s[%d]", prefix, i), e)
					continue
				}
				walk(prefix, e)
			}
		case map[string]interface{}:
			if isGrouping(v) {
				for k, e := range v {
					walk(prefix+"."+k, e)
				}
				return
			}
			b, _ := json.Marshal(v)
			rows = append(rows, prefix+" "+string(b))
		default:
			b, _ := json.Marshal(v)
			rows = append(rows, prefix+" "+string(b))
		}
	}
	walk("", tree)
	sort.Strings(rows)
	return rows
}

// isGrouping tells a map of source categories to results from a row: its values are slices.
func isGrouping(m map[string]interface{}) bool {
	for _, v := range m {
		if _, ok := v.([]interface{}); ok {
			return true
		}
	}
	return false
}
