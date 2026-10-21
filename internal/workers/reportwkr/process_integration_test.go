package reportwkr

// Integration tests running Worker.process against a real PostgreSQL and Redis. They are skipped unless
// PENGUIN_TEST_POSTGRES_DSN and PENGUIN_TEST_REDIS_URL point at disposable ones, see internal/repo/integration_test.go
// and internal/workers/calcwkr/lock_test.go. Every test works in a schema of its own, which it drops afterwards.

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/goccy/go-json"
	"github.com/redis/go-redis/v9"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/service"
	"exusiai.dev/backend-next/internal/util/reportverifs"
)

// testSchema is the subset of the production schema the report worker writes and reads.
const testSchema = `
CREATE TYPE server AS ENUM ('CN', 'US', 'JP', 'KR');
CREATE TABLE drop_reports (
	report_id bigserial PRIMARY KEY,
	stage_id integer NOT NULL,
	pattern_id bigint NOT NULL,
	times integer NOT NULL,
	created_at timestamp with time zone DEFAULT now() NOT NULL,
	reliability integer NOT NULL,
	server server NOT NULL,
	account_id bigint NOT NULL,
	source_name text,
	version text
);
CREATE INDEX drop_reports_account_id ON drop_reports (account_id);
CREATE INDEX drop_reports_created_at ON drop_reports (created_at);
CREATE TABLE drop_report_extras (
	report_id bigint PRIMARY KEY,
	ip cidr NOT NULL,
	metadata jsonb,
	md5 character varying(32)
);
CREATE TABLE drop_patterns (
	pattern_id bigserial PRIMARY KEY,
	hash character varying(64) NOT NULL,
	original_fingerprint text
);
CREATE UNIQUE INDEX drop_patterns_hash ON drop_patterns (hash);
CREATE TABLE drop_pattern_elements (
	element_id bigserial PRIMARY KEY,
	drop_pattern_id bigint NOT NULL,
	item_id integer NOT NULL,
	quantity integer NOT NULL
);
CREATE TABLE stages (
	stage_id serial PRIMARY KEY,
	ark_stage_id character varying(255) NOT NULL,
	zone_id integer NOT NULL,
	stage_type text NOT NULL,
	extra_process_type text,
	code jsonb,
	sanity integer,
	existence jsonb,
	min_clear_time integer
);
INSERT INTO stages (ark_stage_id, zone_id, stage_type) VALUES ('main_01-07', 1, 'MAIN'), ('main_04-04', 4, 'MAIN');
`

// newTestWorker returns a worker persisting to a fresh schema on PENGUIN_TEST_POSTGRES_DSN holding testSchema, and
// setting the recall keys on PENGUIN_TEST_REDIS_URL. Reports are not verified: all get reliability 0.
func newTestWorker(t *testing.T) *Worker {
	t.Helper()
	dsn := os.Getenv("PENGUIN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PENGUIN_TEST_POSTGRES_DSN is not set")
	}
	redisURL := os.Getenv("PENGUIN_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("PENGUIN_TEST_REDIS_URL is not set")
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
	if _, err := db.ExecContext(ctx, testSchema); err != nil {
		t.Fatal(err)
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { rdb.Close() })

	// stages are cached by ark stage id across tests
	cache.Initialize()
	if err := cache.StageByArkID.Flush(); err != nil {
		t.Fatal(err)
	}

	return &Worker{WorkerDeps: WorkerDeps{
		DB:                     db,
		Redis:                  rdb,
		StageService:           service.NewStage(repo.NewStage(db)),
		DropReportRepo:         repo.NewDropReport(db),
		DropPatternRepo:        repo.NewDropPattern(db),
		DropReportExtraRepo:    repo.NewDropReportExtra(db),
		DropPatternElementRepo: repo.NewDropPatternElement(db),
		ReportVerifier:         &reportverifs.ReportVerifiers{},
	}}
}

// newTestTask returns a task of the account created at createdAt, encoded as it is published, with a task id of its
// own whose recall key is deleted after the test.
func newTestTask(t *testing.T, w *Worker, accountId int, createdAt time.Time, ip string, reports ...*types.ReportTaskSingleReport) []byte {
	t.Helper()
	task := &types.ReportTask{
		TaskID:    "test-" + strconv.FormatUint(rand.Uint64(), 36),
		CreatedAt: createdAt.UnixMicro(),
		FragmentReportCommon: types.FragmentReportCommon{
			Server:  "CN",
			Source:  "MeoAssistant",
			Version: "v4.0.0",
		},
		Reports:   reports,
		AccountID: accountId,
		IP:        ip,
	}
	t.Cleanup(func() { w.Redis.Del(context.Background(), constant.ReportRedisPrefix+task.TaskID) })
	b, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// processTask decodes the task and processes it, as each delivery of the message does.
func processTask(t *testing.T, w *Worker, data []byte) error {
	task := &types.ReportTask{}
	if err := json.Unmarshal(data, task); err != nil {
		t.Error(err)
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return w.process(ctx, task)
}

func taskID(t *testing.T, data []byte) string {
	t.Helper()
	task := &types.ReportTask{}
	if err := json.Unmarshal(data, task); err != nil {
		t.Fatal(err)
	}
	return task.TaskID
}

// Row dumps: every stored column as text, NULL spelled out. Pattern elements are inserted in a random order, so their
// ids are left out.
const (
	dumpDropReports = `SELECT concat_ws('|', report_id, stage_id, pattern_id, times,
		(extract(epoch FROM created_at) * 1000000)::bigint, reliability, server, account_id,
		coalesce(source_name, 'NULL'), coalesce(version, 'NULL')) FROM drop_reports ORDER BY report_id`
	dumpDropReportExtras = `SELECT concat_ws('|', report_id, ip, coalesce(metadata::text, 'NULL'), coalesce(md5, 'NULL'))
		FROM drop_report_extras ORDER BY report_id`
	dumpDropPatterns = `SELECT concat_ws('|', pattern_id, hash, coalesce(original_fingerprint, 'NULL'))
		FROM drop_patterns ORDER BY pattern_id`
	dumpDropPatternElements = `SELECT concat_ws('|', drop_pattern_id, item_id, quantity)
		FROM drop_pattern_elements ORDER BY drop_pattern_id, item_id`
)

func dumpRows(t *testing.T, db bun.IDB, query string) []string {
	t.Helper()
	rows := []string{}
	if err := db.NewRaw(query).Scan(context.Background(), &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func assertRows(t *testing.T, db bun.IDB, query string, want []string) {
	t.Helper()
	if got := dumpRows(t, db, query); !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", query, got, want)
	}
}

func assertRecallKey(t *testing.T, w *Worker, taskId string, want string) {
	t.Helper()
	got, err := w.Redis.Get(context.Background(), constant.ReportRedisPrefix+taskId).Result()
	if err != nil {
		t.Fatalf("recall key of %s: %v", taskId, err)
	}
	if got != want {
		t.Errorf("recall key of %s = %q, want %q", taskId, got, want)
	}
}

// TestProcessStoresReports pins the rows a batch task and a singular task store, column by column: the drops merged
// by item, a new pattern with its elements and an existing one without, the metadata and md5, and the IP fallback.
func TestProcessStoresReports(t *testing.T) {
	w := newTestWorker(t)
	createdAt := time.UnixMicro(1760600000123456)

	batch := newTestTask(t, w, 42, createdAt, "",
		&types.ReportTaskSingleReport{
			FragmentStageID: types.FragmentStageID{StageID: "main_01-07"},
			Drops: []*types.Drop{
				{DropType: "REGULAR", ItemID: 30012, Quantity: 2},
				{DropType: "EXTRA", ItemID: 30012, Quantity: 1},
				{DropType: "SPECIAL", ItemID: 30062, Quantity: 1},
			},
			Times: 1,
			Metadata: &types.ReportRequestMetadata{
				Fingerprint: "fp",
				MD5:         "0123456789abcdef0123456789abcdef",
				FileName:    "a.png",
			},
		},
		&types.ReportTaskSingleReport{
			FragmentStageID: types.FragmentStageID{StageID: "main_04-04"},
			Drops:           []*types.Drop{{DropType: "REGULAR", ItemID: 30013, Quantity: 1}},
			Times:           1,
			Metadata:        &types.ReportRequestMetadata{RecognizerVersion: "v1.2.3"},
		},
	)
	singular := newTestTask(t, w, 42, createdAt.Add(time.Second), "203.0.113.7",
		&types.ReportTaskSingleReport{
			FragmentStageID: types.FragmentStageID{StageID: "main_01-07"},
			Drops: []*types.Drop{
				{DropType: "SPECIAL", ItemID: 30062, Quantity: 1},
				{DropType: "REGULAR", ItemID: 30012, Quantity: 3},
			},
			Times: 2,
		},
	)
	for _, task := range [][]byte{batch, singular} {
		if err := processTask(t, w, task); err != nil {
			t.Fatal(err)
		}
	}

	assertRows(t, w.DB, dumpDropReports, []string{
		"1|1|1|1|1760600000123456|0|CN|42|MeoAssistant|v4.0.0",
		"2|2|2|1|1760600000123456|0|CN|42|MeoAssistant|v4.0.0",
		"3|1|1|2|1760600001123456|0|CN|42|MeoAssistant|v4.0.0",
	})
	assertRows(t, w.DB, dumpDropReportExtras, []string{
		`1|127.0.0.1/32|{"md5": "0123456789abcdef0123456789abcdef", "fileName": "a.png", "fingerprint": "fp"}|0123456789abcdef0123456789abcdef`,
		`2|127.0.0.1/32|{"recognizerVersion": "v1.2.3"}|NULL`,
		`3|203.0.113.7/32|NULL|NULL`,
	})
	assertRows(t, w.DB, dumpDropPatterns, []string{
		"1|e00e0412ac5b0b34|30012:3|30062:1",
		"2|efe7d317a0df5d30|30013:1",
	})
	assertRows(t, w.DB, dumpDropPatternElements, []string{
		"1|30012|3",
		"1|30062|1",
		"2|30013|1",
	})
	assertRecallKey(t, w, taskID(t, batch), "2")
	assertRecallKey(t, w, taskID(t, singular), "3")
}
