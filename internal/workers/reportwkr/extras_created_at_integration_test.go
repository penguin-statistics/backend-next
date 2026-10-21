package reportwkr

// Integration tests of drop_report_extras.created_at with and without ReportSchemaV2Enabled, skipped unless
// PENGUIN_TEST_POSTGRES_DSN and PENGUIN_TEST_REDIS_URL are set (see process_integration_test.go). The flag off on
// today's schema is pinned by TestProcessStoresReports.

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/service"
)

// newDBTestSchema is the schema of the new database (scripts/newdb) with the partitions of 2026-10.
func newDBTestSchema(t *testing.T) string {
	t.Helper()
	ddl := ""
	for _, file := range []string{"schema.sql", "indexes.sql"} {
		b, err := os.ReadFile("../../../scripts/newdb/" + file)
		if err != nil {
			t.Fatal(err)
		}
		ddl += string(b) + "\n"
	}
	return ddl + `
SELECT create_report_partitions('2026-10-01');
`
}

// newDBDropReportsSchema is newDBTestSchema with the partitions of 2026-11 and the rows the foreign keys of drop_reports
// and drop_pattern_elements need for the reports of mainReport and recruitReport. The recruit stage has drop infos,
// so that its reports pass the verifier.
func newDBDropReportsSchema(t *testing.T) string {
	return newDBTestSchema(t) + `
SELECT create_report_partitions('2026-11-01');
INSERT INTO accounts (account_id, penguin_id, weight, created_at) VALUES (42, '42', 1, now());
INSERT INTO items (item_id, ark_item_id, existence, name, sort_id, rarity, keywords) VALUES
	(30012, '30012', '{}', '{}', 0, 0, '{}'), (30062, '30062', '{}', '{}', 0, 0, '{}'),
	(96, 'tag_96', '{}', '{}', 0, 0, '{}'), (103, 'tag_103', '{}', '{}', 0, 0, '{}');
INSERT INTO zones (zone_id, ark_zone_id, index, category, name, existence) VALUES
	(1, 'main_1', 1, 'MAINLINE', '{}', '{}'), (110, 'recruit', 0, 'RECRUIT', '{}', '{}');
INSERT INTO stages (stage_id, ark_stage_id, zone_id, code, existence) VALUES
	(1, 'main_01-07', 1, '{}', '{}'), (788, 'recruit', 110, '{}', '{}');
INSERT INTO time_ranges (range_id, start_time, end_time, server) VALUES (1, '-infinity', 'infinity', 'CN');
INSERT INTO drop_infos (server, stage_id, item_id, drop_type, range_id, bounds, accumulable) VALUES
	('CN', 788, NULL, 'REGULAR', 1, '{"lower": 0, "upper": 5}', true),
	('CN', 788, 96, 'REGULAR', 1, '{"lower": 0, "upper": 1}', true),
	('CN', 788, 103, 'REGULAR', 1, '{"lower": 0, "upper": 1}', true);
`
}

// newSchemaTestWorker is newTestWorker with ddl as the schema and reportSchemaV2 set.
func newSchemaTestWorker(t *testing.T, ddl string, reportSchemaV2 bool) *Worker {
	t.Helper()
	w := newTestWorker(t)
	if ddl == testSchema {
		w.reportSchemaV2 = reportSchemaV2
		return w
	}

	// the schema newTestWorker made is left unused
	dsn := os.Getenv("PENGUIN_TEST_POSTGRES_DSN")
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
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	if err := cache.StageByArkID.Flush(); err != nil {
		t.Fatal(err)
	}

	nw := newWorker(WorkerDeps{
		DB:                     db,
		Redis:                  w.Redis,
		StageService:           service.NewStage(repo.NewStage(db)),
		DropReportRepo:         repo.NewDropReport(db),
		DropPatternRepo:        repo.NewDropPattern(db),
		DropReportExtraRepo:    repo.NewDropReportExtra(db),
		DropPatternElementRepo: repo.NewDropPatternElement(db),
		DropInfoRepo:           repo.NewDropInfo(db),
		RejectRuleRepo:         repo.NewRejectRule(db),
	}, defaultConsumerConfig)
	nw.reportSchemaV2 = reportSchemaV2
	return nw
}

func mainReport(metadata *types.ReportRequestMetadata, drops ...*types.Drop) *types.ReportTaskSingleReport {
	return &types.ReportTaskSingleReport{
		FragmentStageID: types.FragmentStageID{StageID: "main_01-07"},
		Drops:           drops,
		Times:           1,
		Metadata:        metadata,
	}
}

func recruitReport(drops ...*types.Drop) *types.ReportTaskSingleReport {
	return &types.ReportTaskSingleReport{
		FragmentStageID: types.FragmentStageID{StageID: "recruit"},
		Drops:           drops,
		Times:           1,
	}
}

// dumpDropReportExtrasCreatedAt is dumpDropReportExtras with the partition and created_at of each row, and whether
// created_at is the one of its report.
const dumpDropReportExtrasCreatedAt = `SELECT concat_ws('|', dre.tableoid::regclass, dre.report_id, dre.ip,
	coalesce(dre.metadata::text, 'NULL'), coalesce(dre.md5, 'NULL'), (extract(epoch FROM dre.created_at) * 1000000)::bigint,
	dre.created_at = dr.created_at)
	FROM drop_report_extras dre LEFT JOIN drop_reports dr ON dr.report_id = dre.report_id ORDER BY dre.report_id`

// TestProcessWritesDropReportExtrasCreatedAt pins drop_report_extras with the flag on the schema of the new database:
// created_at is the one of the report, so a report and its extra land in the partitions of the same month, the last
// microsecond of a month included. The reads of drop_report_extras still work there and leave CreatedAt nil.
func TestProcessWritesDropReportExtrasCreatedAt(t *testing.T) {
	w := newSchemaTestWorker(t, newDBDropReportsSchema(t), true)
	endOfOctober := time.Date(2026, 10, 31, 23, 59, 59, 999999000, time.UTC)

	batch := newTestTask(t, w, 42, endOfOctober, "203.0.113.7",
		mainReport(&types.ReportRequestMetadata{MD5: "0123456789abcdef0123456789abcdef"},
			&types.Drop{DropType: "REGULAR", ItemID: 30012, Quantity: 2}),
		mainReport(nil, &types.Drop{DropType: "SPECIAL", ItemID: 30062, Quantity: 1}),
	)
	// the first microsecond of November, and the IP fallback
	singular := newTestTask(t, w, 42, endOfOctober.Add(time.Microsecond), "",
		mainReport(nil, &types.Drop{DropType: "REGULAR", ItemID: 30012, Quantity: 2}))
	for _, task := range [][]byte{batch, singular} {
		if err := processTask(t, w, task); err != nil {
			t.Fatal(err)
		}
	}

	assertRows(t, w.DB, `SELECT concat_ws('|', tableoid::regclass, report_id) FROM drop_reports ORDER BY report_id`, []string{
		"drop_reports_2026_10|4988523",
		"drop_reports_2026_10|4988524",
		"drop_reports_2026_11|4988525",
	})
	assertRows(t, w.DB, dumpDropReportExtrasCreatedAt, []string{
		`drop_report_extras_2026_10|4988523|203.0.113.7/32|{"md5": "0123456789abcdef0123456789abcdef"}|0123456789abcdef0123456789abcdef|1793491199999999|t`,
		`drop_report_extras_2026_10|4988524|203.0.113.7/32|NULL|NULL|1793491199999999|t`,
		`drop_report_extras_2026_11|4988525|127.0.0.1/32|NULL|NULL|1793491200000000|t`,
	})
	assertRecallKey(t, w, taskID(t, batch), "4988524")
	assertRecallKey(t, w, taskID(t, singular), "4988525")

	ctx := context.Background()
	extra, err := w.DropReportExtraRepo.GetDropReportExtraById(ctx, 4988525)
	if err != nil {
		t.Fatal(err)
	}
	if extra.ReportID != 4988525 || extra.IP != "127.0.0.1/32" || extra.CreatedAt != nil {
		t.Errorf("GetDropReportExtraById = %+v", extra)
	}
}

// TestProcessStoresRecruitReportsInDropReports pins that the flag changes nothing else: on the new database a
// recruitment report is a drop_reports row of the recruit stage with its drop pattern and a drop_report_extras row
// with the created_at of the report, as every other report.
func TestProcessStoresRecruitReportsInDropReports(t *testing.T) {
	w := newSchemaTestWorker(t, newDBDropReportsSchema(t), true)
	createdAt := time.Date(2026, 10, 16, 8, 0, 0, 123456000, time.UTC)

	task := newTestTask(t, w, 42, createdAt, "203.0.113.7", recruitReport(
		&types.Drop{DropType: "REGULAR", ItemID: 103, Quantity: 1},
		&types.Drop{DropType: "REGULAR", ItemID: 96, Quantity: 1},
	))
	if err := processTask(t, w, task); err != nil {
		t.Fatal(err)
	}
	assertRows(t, w.DB, `SELECT concat_ws('|', tableoid::regclass, report_id, stage_id, pattern_id, times,
		(extract(epoch FROM created_at) * 1000000)::bigint, reliability, server, account_id) FROM drop_reports`, []string{
		"drop_reports_2026_10|4988523|788|1|1|1792137600123456|0|CN|42",
	})
	assertRows(t, w.DB, dumpDropReportExtrasCreatedAt, []string{
		"drop_report_extras_2026_10|4988523|203.0.113.7/32|NULL|NULL|1792137600123456|t",
	})
	assertRows(t, w.DB, dumpDropPatternElements, []string{"1|96|1", "1|103|1"})
	assertRecallKey(t, w, taskID(t, task), "4988523")
}

// TestProcessDropReportExtrasCreatedAtFollowsFlag requires the flag to match the schema: off on the new database, an
// extra without created_at fails to insert (no partition takes a NULL created_at); on today's database, an extra with
// created_at fails to insert. Either rolls the whole task back.
func TestProcessDropReportExtrasCreatedAtFollowsFlag(t *testing.T) {
	for _, tc := range []struct {
		name           string
		ddl            string
		reportSchemaV2 bool
		wantErr        string
	}{
		{"flag off on the new database", newDBDropReportsSchema(t), false, `no partition of relation "drop_report_extras"`},
		{"flag on on today's database", testSchema, true, `column "created_at" of relation "drop_report_extras" does not exist`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newSchemaTestWorker(t, tc.ddl, tc.reportSchemaV2)
			task := newTestTask(t, w, 42, time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC), "203.0.113.7",
				mainReport(nil, &types.Drop{DropType: "REGULAR", ItemID: 30012, Quantity: 2}))
			if err := processTask(t, w, task); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got error %v, want one containing %q", err, tc.wantErr)
			}
			assertRows(t, w.DB, dumpDropReports, []string{})
			assertRows(t, w.DB, dumpDropReportExtras, []string{})
		})
	}
}

// TestCheckReportSchema requires the startup check to pass only when the flag matches the schema, so that a mismatch
// fails the startup instead of every report task.
func TestCheckReportSchema(t *testing.T) {
	for _, tc := range []struct {
		name           string
		ddl            string
		reportSchemaV2 bool
		wantErr        bool
	}{
		{"flag on on the new database", newDBTestSchema(t), true, false},
		{"flag off on today's database", testSchema, false, false},
		{"flag off on the new database", newDBTestSchema(t), false, true},
		{"flag on on today's database", testSchema, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newSchemaTestWorker(t, tc.ddl, tc.reportSchemaV2)
			err := w.checkReportSchema(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("got error %v, want an error: %t", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "PENGUIN_V3_REPORT_SCHEMA_V2_ENABLED") {
				t.Errorf("the error does not name the flag: %v", err)
			}
		})
	}
}

// TestEnsureReportPartitions requires the worker to create the partitions of the current and the next month (UTC) of
// both report tables on the new database, and to leave existing ones alone when it runs again.
func TestEnsureReportPartitions(t *testing.T) {
	w := newSchemaTestWorker(t, newDBTestSchema(t), true)
	const partitions = `SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
		WHERE i.inhparent IN ('drop_reports'::regclass, 'drop_report_extras'::regclass)
		ORDER BY 1`
	// the last microsecond of 2026-12 in UTC is already 2027-01 in Asia/Shanghai: the months are UTC ones
	now := time.Date(2026, 12, 31, 23, 59, 59, 999999000, time.UTC).In(time.FixedZone("UTC+8", 8*3600))
	for i := 0; i < 2; i++ {
		if err := w.ensureReportPartitions(context.Background(), now); err != nil {
			t.Fatal(err)
		}
		assertRows(t, w.DB, partitions, []string{
			"drop_report_extras_2026_10", "drop_report_extras_2026_12", "drop_report_extras_2027_01",
			"drop_reports_2026_10", "drop_reports_2026_12", "drop_reports_2027_01",
		})
	}
	// a row of the last microsecond of the next month lands in its partition
	if _, err := w.DB.ExecContext(context.Background(), `INSERT INTO drop_report_extras (report_id, ip, created_at)
		VALUES (1, '127.0.0.1', '2027-01-31 23:59:59.999999+00')`); err != nil {
		t.Fatal(err)
	}
}
