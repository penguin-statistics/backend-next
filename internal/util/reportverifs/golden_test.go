package reportverifs

// TestReliabilityGolden runs the verification against a real PostgreSQL, and is skipped unless
// PENGUIN_TEST_POSTGRES_DSN points at a disposable one (see internal/repo/integration_test.go). It works in a schema of
// its own, which it drops afterwards.

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/repo"
)

// goldenSchema is the subset of the production schema the verification reads.
const goldenSchema = `
CREATE TABLE stages (
	stage_id integer PRIMARY KEY,
	ark_stage_id character varying(32) NOT NULL
);
CREATE TABLE items (
	item_id integer PRIMARY KEY
);
CREATE TABLE time_ranges (
	range_id bigint PRIMARY KEY,
	start_time timestamp with time zone NOT NULL,
	end_time timestamp with time zone NOT NULL,
	server text NOT NULL
);
CREATE TABLE drop_infos (
	drop_id bigint PRIMARY KEY,
	server text NOT NULL,
	stage_id integer NOT NULL,
	item_id integer,
	drop_type text NOT NULL,
	range_id bigint NOT NULL,
	bounds jsonb,
	accumulable boolean NOT NULL,
	extras jsonb
);
CREATE TABLE reject_rules (
	rule_id integer PRIMARY KEY,
	created_at timestamp with time zone DEFAULT now() NOT NULL,
	updated_at timestamp with time zone,
	status integer,
	expr text NOT NULL,
	with_reliability integer NOT NULL
);
`

// goldenFixture is testdata/golden_reports.json, exported from production by testdata/golden_reports.sql: recent
// report tasks with the reliability stored for each report, and the drop infos and reject rules they were verified
// with.
type goldenFixture struct {
	Stages      json.RawMessage `json:"stages"`
	TimeRanges  json.RawMessage `json:"timeRanges"`
	DropInfos   json.RawMessage `json:"dropInfos"`
	RejectRules json.RawMessage `json:"rejectRules"`
	Tasks       []struct {
		CreatedAt int64  `json:"createdAt"`
		Server    string `json:"server"`
		Source    string `json:"source"`
		Version   string `json:"version"`
		AccountID int    `json:"accountId"`
		Reports   []struct {
			types.ReportTaskSingleReport
			Reliability int `json:"reliability"`
		} `json:"reports"`
	} `json:"tasks"`
}

// loadGolden reads the fixture into a fresh schema on PENGUIN_TEST_POSTGRES_DSN. Every time range it holds was current
// when the reports of its stage were created, so it is made current until the end of time.
func loadGolden(t *testing.T) (*bun.DB, *goldenFixture) {
	t.Helper()
	dsn := os.Getenv("PENGUIN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PENGUIN_TEST_POSTGRES_DSN is not set")
	}
	b, err := os.ReadFile("testdata/golden_reports.json")
	if err != nil {
		t.Fatal(err)
	}
	var f goldenFixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	schema := fmt.Sprintf("it_%d_%d", time.Now().UnixNano(), rand.Int63())
	admin := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn))), pgdialect.New())
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	db := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(
		pgdriver.WithDSN(dsn),
		pgdriver.WithConnParams(map[string]interface{}{"search_path": schema}),
	)), pgdialect.New())
	t.Cleanup(func() { db.Close() })

	for _, q := range []struct {
		query string
		rows  json.RawMessage
	}{
		{goldenSchema, nil},
		{`INSERT INTO stages SELECT * FROM jsonb_to_recordset(?::jsonb) AS x("stageId" integer, "arkStageId" text)`, f.Stages},
		{`INSERT INTO time_ranges SELECT "rangeId", "startTime", 'infinity', server
			FROM jsonb_to_recordset(?::jsonb) AS x("rangeId" bigint, "startTime" timestamptz, server text)`, f.TimeRanges},
		{`INSERT INTO drop_infos SELECT "dropId", server, "stageId", "itemId", "dropType", "rangeId", bounds, accumulable
			FROM jsonb_to_recordset(?::jsonb) AS x("dropId" bigint, server text, "stageId" integer, "itemId" integer,
				"dropType" text, "rangeId" bigint, bounds jsonb, accumulable boolean)`, f.DropInfos},
		{`INSERT INTO reject_rules (rule_id, status, expr, with_reliability) SELECT "ruleId", status, expr, "withReliability"
			FROM jsonb_to_recordset(?::jsonb) AS x("ruleId" integer, status integer, expr text, "withReliability" integer)`, f.RejectRules},
	} {
		var args []any
		if q.rows != nil {
			args = append(args, string(q.rows))
		}
		if _, err := db.ExecContext(ctx, q.query, args...); err != nil {
			t.Fatal(err)
		}
	}
	return db, &f
}

// task returns the i-th task of the fixture, as the worker decodes it.
func (f *goldenFixture) task(i int) *types.ReportTask {
	ft := f.Tasks[i]
	task := &types.ReportTask{
		TaskID:    fmt.Sprintf("golden-%d", i),
		CreatedAt: ft.CreatedAt,
		FragmentReportCommon: types.FragmentReportCommon{
			Server:  ft.Server,
			Source:  ft.Source,
			Version: ft.Version,
		},
		AccountID: ft.AccountID,
		IP:        "198.51.100.1/32",
	}
	for _, r := range ft.Reports {
		r := r.ReportTaskSingleReport
		task.Reports = append(task.Reports, &r)
	}
	return task
}

// acceptedSince580 are the tasks of the fixture, by creation time, whose one report production rejected (6) and the
// verification accepts (0) since #580: main_01-07 on CN by MAA, times above 1, with exactly times + 1 kinds of EXTRA
// items (item 1 every time, and another one each time). The upper bound of the drop type was times then.
var acceptedSince580 = map[int64]struct{}{
	1792442045923410: {}, // times 2, 3 kinds of EXTRA items
	1792442087171325: {}, // times 3, 4 kinds
	1792442691974785: {}, // times 2, 3 kinds
	1792442770717793: {}, // times 2, 3 kinds
	1792442980676064: {}, // times 2, 3 kinds
	1792443237162180: {}, // times 2, 3 kinds
	1792443293505065: {}, // times 2, 3 kinds
	1792443469399203: {}, // times 2, 3 kinds
	1792443582307508: {}, // times 2, 3 kinds
	1792443680911074: {}, // times 2, 3 kinds
	1792444005907837: {}, // times 2, 3 kinds
	1792444055595097: {}, // times 2, 3 kinds
	1792444231823050: {}, // times 2, 3 kinds
	1792444262877090: {}, // times 2, 3 kinds
	1792444296838620: {}, // times 4, 5 kinds
	1792444317484411: {}, // times 2, 3 kinds
	1792444411856978: {}, // times 2, 3 kinds
	1792444441852630: {}, // times 2, 3 kinds
	1792444498974912: {}, // times 4, 5 kinds
	1792444680651388: {}, // times 2, 3 kinds
	1792444884750042: {}, // times 2, 3 kinds
	1792446076522142: {}, // times 2, 3 kinds
	1792446163818001: {}, // times 2, 3 kinds
}

// TestReliabilityGolden verifies the report tasks of the fixture, and checks that each report gets the reliability
// stored for it in production, except the reports in acceptedSince580, which now get 0.
func TestReliabilityGolden(t *testing.T) {
	db, f := loadGolden(t)
	v := &Verifier{DropInfoRepo: repo.NewDropInfo(db), RejectRuleRepo: repo.NewRejectRule(db)}
	ctx := context.Background()

	counts := map[int]int{}
	accepted := 0
	for i, ft := range f.Tasks {
		task := f.task(i)
		for idx, report := range task.Reports {
			got, message, err := v.Reliability(ctx, task, report)
			if err != nil {
				t.Fatal(err)
			}
			counts[got]++
			want := ft.Reports[idx].Reliability
			if _, ok := acceptedSince580[ft.CreatedAt]; ok {
				if want != 6 {
					t.Errorf("task %d (created at %d), report %d: stored %d, not 6", i, ft.CreatedAt, idx, want)
				}
				want = 0
				accepted++
			}
			if got != want {
				t.Errorf("task %d (created at %d), report %d: reliability %d, want %d", i, ft.CreatedAt, idx, got, want)
			}
			// the messages equalled the pipeline's while it existed: here, a rejected report has one
			if (message != "") != (got != 0) {
				t.Errorf("task %d (created at %d), report %d: reliability %d with message %q", i, ft.CreatedAt, idx, got, message)
			}
		}
	}
	if accepted != len(acceptedSince580) {
		t.Errorf("%d reports of acceptedSince580 verified, want %d", accepted, len(acceptedSince580))
	}
	t.Logf("%d tasks verified; reports by reliability: %v", len(f.Tasks), counts)
}

// TestReliabilityDatabaseError checks that a database error fails the verification, where it gave reliability 6 or 7
// before Verifier, and is not cached: the verification reads again on the next report.
func TestReliabilityDatabaseError(t *testing.T) {
	db, f := loadGolden(t)
	ctx := context.Background()
	// the first task of reliability 0 passes the drop check, so that both checks run
	task := f.task(0)
	for i := range f.Tasks {
		if f.Tasks[i].Reports[0].Reliability == 0 {
			task = f.task(i)
			break
		}
	}

	for _, table := range []string{"reject_rules", "drop_infos"} {
		// a Verifier of its own, which has read nothing
		v := &Verifier{DropInfoRepo: repo.NewDropInfo(db), RejectRuleRepo: repo.NewRejectRule(db)}
		if _, err := db.ExecContext(ctx, "ALTER TABLE "+table+" RENAME TO "+table+"_gone"); err != nil {
			t.Fatal(err)
		}
		if r, _, err := v.Reliability(ctx, task, task.Reports[0]); err == nil {
			t.Errorf("without %s: reliability %d, want an error", table, r)
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE "+table+"_gone RENAME TO "+table); err != nil {
			t.Fatal(err)
		}
		if r, _, err := v.Reliability(ctx, task, task.Reports[0]); err != nil || r != 0 {
			t.Errorf("with %s again: reliability %d, %v, want 0", table, r, err)
		}
	}
}
