package reportwkr

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/goccy/go-json"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/redis/go-redis/v9"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/service"
	"exusiai.dev/backend-next/internal/util/reportverifs"
)

// newTestMsg returns a message carrying the reply subject of a JetStream message delivered numDelivered times. It is
// bound to no connection, so acking it fails.
func newTestMsg(subject string, numDelivered int, data []byte) *nats.Msg {
	return &nats.Msg{
		Subject: subject,
		Reply:   fmt.Sprintf("$JS.ACK.test-stream.test-consumer.%d.1.1.%d.0", numDelivered, time.Now().UnixNano()),
		Sub:     &nats.Subscription{},
		Data:    data,
	}
}

// consumeDeliveries returns the current state of the consume_num_delivered histogram.
func consumeDeliveries(t *testing.T) *dto.Histogram {
	t.Helper()
	m := &dto.Metric{}
	if err := observability.ReportConsumeNumDelivered.Write(m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram()
}

func TestConsumeMetricsOnUnmarshalFailure(t *testing.T) {
	consumed := observability.ReportConsumeTotal.WithLabelValues("failed_unmarshal", "REPORT.SINGLE")
	before := testutil.ToFloat64(consumed)
	ackFailuresBefore := testutil.ToFloat64(observability.ReportConsumeAckFailures)
	deliveriesBefore := consumeDeliveries(t)

	w := &Worker{}
	if err := w.ingestPreprocess(context.Background(), newTestMsg("REPORT.SINGLE", 3, []byte("{"))); err == nil {
		t.Fatal("expected an unmarshal error")
	}

	if got := testutil.ToFloat64(consumed) - before; got != 1 {
		t.Errorf("consume_total{failed_unmarshal,REPORT.SINGLE} increased by %v, want 1", got)
	}
	if got := testutil.ToFloat64(observability.ReportConsumeAckFailures) - ackFailuresBefore; got != 1 {
		t.Errorf("consume_ack_failures_total increased by %v, want 1", got)
	}
	// the message is delivered for the third time while its stream and consumer sequences are 1
	deliveries := consumeDeliveries(t)
	if got := deliveries.GetSampleCount() - deliveriesBefore.GetSampleCount(); got != 1 {
		t.Errorf("consume_num_delivered observed %d samples, want 1", got)
	}
	if got := deliveries.GetSampleSum() - deliveriesBefore.GetSampleSum(); got != 3 {
		t.Errorf("consume_num_delivered observed %v, want 3", got)
	}
}

func TestConsumeMetricsOnProcessFailure(t *testing.T) {
	consumed := observability.ReportConsumeTotal.WithLabelValues("failed_process", "other")
	stored := observability.ReportStoredTotal.WithLabelValues("other")
	before, storedBefore := testutil.ToFloat64(consumed), testutil.ToFloat64(stored)

	// a closed database fails the transaction before anything is stored
	db := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN("postgres://test@127.0.0.1:1/test?sslmode=disable"))), pgdialect.New())
	db.Close()
	w := newWorker(WorkerDeps{DB: db, ReportVerifier: &reportverifs.ReportVerifiers{}}, defaultConsumerConfig)
	data, _ := json.Marshal(&types.ReportTask{TaskID: "test"})
	if err := w.ingestPreprocess(context.Background(), newTestMsg("REPORT.UNKNOWN", 1, data)); err == nil {
		t.Fatal("expected a process error")
	}

	if got := testutil.ToFloat64(consumed) - before; got != 1 {
		t.Errorf("consume_total{failed_process,other} increased by %v, want 1", got)
	}
	if got := testutil.ToFloat64(stored) - storedBefore; got != 0 {
		t.Errorf("stored_total{other} increased by %v, want 0", got)
	}
}

// TestConsumeMetricsOnStore stores a batch on a real PostgreSQL and Redis, once successfully and once failing on the
// stage of its second report after the first one is inserted. It is skipped unless PENGUIN_TEST_POSTGRES_DSN and
// PENGUIN_TEST_REDIS_URL point at disposable ones, see internal/repo/integration_test.go.
func TestConsumeMetricsOnStore(t *testing.T) {
	dsn, redisURL := os.Getenv("PENGUIN_TEST_POSTGRES_DSN"), os.Getenv("PENGUIN_TEST_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("PENGUIN_TEST_POSTGRES_DSN or PENGUIN_TEST_REDIS_URL is not set")
	}
	ctx := context.Background()
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { rdb.Close() })

	cache.Initialize()
	cache.StageByArkID.Set("main_01-07", model.Stage{StageID: 1, ArkStageID: "main_01-07"}, time.Minute)
	for _, tc := range []struct {
		name        string
		secondStage string
		outcome     string
		storedDelta float64
		reportsInDB int
		wantErr     bool
	}{
		{"stored", "main_01-07", "stored", 2, 2, false},
		// the stage lookup falls back to a stages table the test schema does not have
		{"rolled back", "main_99-99", "failed_process", 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t, dsn)
			task := &types.ReportTask{
				TaskID:               fmt.Sprintf("test-metrics-%d", time.Now().UnixNano()),
				CreatedAt:            time.Now().UnixMicro(),
				FragmentReportCommon: types.FragmentReportCommon{Server: "CN", Source: "MaaAssistantArknights", Version: "v1"},
				Reports: []*types.ReportTaskSingleReport{
					{FragmentStageID: types.FragmentStageID{StageID: "main_01-07"}, Drops: []*types.Drop{{DropType: "REGULAR", ItemID: 1, Quantity: 2}}, Times: 1},
					{FragmentStageID: types.FragmentStageID{StageID: tc.secondStage}, Drops: []*types.Drop{{DropType: "REGULAR", ItemID: 2, Quantity: 1}}, Times: 1},
				},
				AccountID: 1,
				IP:        "127.0.0.1",
			}
			t.Cleanup(func() { rdb.Del(ctx, constant.ReportRedisPrefix+task.TaskID) })
			data, err := json.Marshal(task)
			if err != nil {
				t.Fatal(err)
			}

			consumed := observability.ReportConsumeTotal.WithLabelValues(tc.outcome, "REPORT.BATCH")
			stored := observability.ReportStoredTotal.WithLabelValues("maa")
			consumedBefore, storedBefore := testutil.ToFloat64(consumed), testutil.ToFloat64(stored)

			w := newWorker(WorkerDeps{
				DB:                     db,
				Redis:                  rdb,
				StageService:           service.NewStage(repo.NewStage(db)),
				DropReportRepo:         repo.NewDropReport(db),
				DropPatternRepo:        repo.NewDropPattern(db),
				DropReportExtraRepo:    repo.NewDropReportExtra(db),
				DropPatternElementRepo: repo.NewDropPatternElement(db),
				ReportVerifier:         &reportverifs.ReportVerifiers{},
			}, defaultConsumerConfig)
			if err := w.ingestPreprocess(ctx, newTestMsg("REPORT.BATCH", 1, data)); (err != nil) != tc.wantErr {
				t.Fatalf("unexpected error %v", err)
			}

			if got := testutil.ToFloat64(consumed) - consumedBefore; got != 1 {
				t.Errorf("consume_total{%s,REPORT.BATCH} increased by %v, want 1", tc.outcome, got)
			}
			if got := testutil.ToFloat64(stored) - storedBefore; got != tc.storedDelta {
				t.Errorf("stored_total{maa} increased by %v, want %v", got, tc.storedDelta)
			}
			if n, err := db.NewSelect().Table("drop_reports").Count(ctx); err != nil || n != tc.reportsInDB {
				t.Errorf("drop_reports has %d rows (err %v), want %d", n, err, tc.reportsInDB)
			}
		})
	}
}

// newTestDB connects to dsn with a fresh schema holding the tables the worker writes, which it drops afterwards.
func newTestDB(t *testing.T, dsn string) *bun.DB {
	t.Helper()
	schema := fmt.Sprintf("it_%d_%d", time.Now().UnixNano(), rand.Int63())
	db := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(
		pgdriver.WithDSN(dsn),
		pgdriver.WithConnParams(map[string]interface{}{"search_path": schema}),
	)), pgdialect.New())
	t.Cleanup(func() {
		if _, err := db.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
		db.Close()
	})
	if _, err := db.Exec("CREATE SCHEMA " + schema + `;
CREATE TYPE server AS ENUM ('CN', 'US', 'JP', 'KR');
CREATE TABLE drop_patterns (pattern_id bigserial PRIMARY KEY, hash character varying(64) NOT NULL UNIQUE, original_fingerprint text);
CREATE TABLE drop_pattern_elements (element_id bigserial PRIMARY KEY, drop_pattern_id bigint NOT NULL, item_id integer NOT NULL, quantity integer NOT NULL);
CREATE TABLE drop_reports (report_id bigserial PRIMARY KEY, stage_id integer NOT NULL, pattern_id bigint NOT NULL, times integer NOT NULL,
	created_at timestamp with time zone DEFAULT now() NOT NULL, reliability integer NOT NULL, server server NOT NULL, account_id bigint NOT NULL,
	source_name text, version text);
CREATE TABLE drop_report_extras (report_id bigint PRIMARY KEY, ip cidr NOT NULL, metadata jsonb, md5 character varying(32));
`); err != nil {
		t.Fatal(err)
	}
	return db
}
