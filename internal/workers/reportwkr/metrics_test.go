package reportwkr

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/goccy/go-json"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/pkg/observability"
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

// unreachableJS is a JetStream context whose publishes fail, as while NATS is unreachable.
type unreachableJS struct {
	nats.JetStreamContext
}

func (unreachableJS) PublishMsg(*nats.Msg, ...nats.PubOpt) (*nats.PubAck, error) {
	return nil, nats.ErrConnectionClosed
}

func (unreachableJS) PublishMsgAsync(*nats.Msg, ...nats.PubOpt) (nats.PubAckFuture, error) {
	return nil, nats.ErrConnectionClosed
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

	// the move to the dead-letter stream fails, and so does the nak that follows it
	w := &Worker{WorkerDeps: WorkerDeps{NatsJS: unreachableJS{}}}
	w.ingestPreprocess(newTestMsg("REPORT.SINGLE", 3, []byte("{")))

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
	stored := observability.ReportStoredTotal.WithLabelValues("other", "other")
	before, storedBefore := testutil.ToFloat64(consumed), testutil.ToFloat64(stored)

	// a closed database fails the transaction before anything is stored
	db := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN("postgres://test@127.0.0.1:1/test?sslmode=disable"))), pgdialect.New())
	db.Close()
	w := newWorker(WorkerDeps{DB: db}, defaultConsumerConfig)
	data, _ := json.Marshal(&types.ReportTask{TaskID: "test"})
	w.ingestPreprocess(newTestMsg("REPORT.UNKNOWN", 1, data))

	if got := testutil.ToFloat64(consumed) - before; got != 1 {
		t.Errorf("consume_total{failed_process,other} increased by %v, want 1", got)
	}
	if got := testutil.ToFloat64(stored) - storedBefore; got != 0 {
		t.Errorf("stored_total{other} increased by %v, want 0", got)
	}
}

// TestConsumeMetricsOnStore stores a batch on a real PostgreSQL and Redis, in the schema of the process tests, once
// successfully and once failing on the stage of its second report, which does not exist, after the first one is
// inserted. It is skipped unless PENGUIN_TEST_POSTGRES_DSN and PENGUIN_TEST_REDIS_URL point at disposable ones, see
// internal/repo/integration_test.go.
func TestConsumeMetricsOnStore(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		secondStage string
		outcome     string
		storedDelta float64
		reportsInDB int
	}{
		{"stored", "main_01-07", "stored", 2, 2},
		{"rolled back", "main_99-99", "failed_process", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newTestWorker(t)
			// a task of a stage that does not exist moves to the dead-letter stream, which fails here: it is naked
			w.NatsJS = unreachableJS{}
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
			t.Cleanup(func() { w.Redis.Del(ctx, constant.ReportRedisPrefix+task.TaskID) })
			data, err := json.Marshal(task)
			if err != nil {
				t.Fatal(err)
			}

			consumed := observability.ReportConsumeTotal.WithLabelValues(tc.outcome, "REPORT.BATCH")
			stored := observability.ReportStoredTotal.WithLabelValues("maa", "CN")
			// the drops of the reports fit no drop info of the schema: both are stored as rejected (6)
			rejected := observability.ReportReliability.WithLabelValues("6", "MaaAssistantArknights", "CN")
			consumedBefore, storedBefore, rejectedBefore := testutil.ToFloat64(consumed), testutil.ToFloat64(stored), testutil.ToFloat64(rejected)

			w.ingestPreprocess(newTestMsg("REPORT.BATCH", 1, data))

			if got := testutil.ToFloat64(consumed) - consumedBefore; got != 1 {
				t.Errorf("consume_total{%s,REPORT.BATCH} increased by %v, want 1", tc.outcome, got)
			}
			if got := testutil.ToFloat64(stored) - storedBefore; got != tc.storedDelta {
				t.Errorf("stored_total{maa,CN} increased by %v, want %v", got, tc.storedDelta)
			}
			// the reports of a task rolled back are not counted either
			if got := testutil.ToFloat64(rejected) - rejectedBefore; got != tc.storedDelta {
				t.Errorf("reliability{6,MaaAssistantArknights,CN} increased by %v, want %v", got, tc.storedDelta)
			}
			if n, err := w.DB.NewSelect().Table("drop_reports").Count(ctx); err != nil || n != tc.reportsInDB {
				t.Errorf("drop_reports has %d rows (err %v), want %d", n, err, tc.reportsInDB)
			}
		})
	}
}
