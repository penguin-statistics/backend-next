package reportwkr

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/model/types"
)

// fakeJetStream counts the calls Start makes to set the consumer up, as on a server that has no consumer yet. The
// other methods are not implemented.
type fakeJetStream struct {
	nats.JetStreamContext
	mu         sync.Mutex
	calls      int
	subscribed int
}

func (f *fakeJetStream) ConsumerInfo(string, string, ...nats.JSOpt) (*nats.ConsumerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return nil, nats.ErrConsumerNotFound
}

func (f *fakeJetStream) AddConsumer(string, *nats.ConsumerConfig, ...nats.JSOpt) (*nats.ConsumerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return &nats.ConsumerInfo{}, nil
}

func (f *fakeJetStream) PullSubscribe(string, string, ...nats.SubOpt) (*nats.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.subscribed++
	return &nats.Subscription{}, nil
}

// hookRecorder is an fx.Lifecycle that records the hooks appended to it and never runs them.
type hookRecorder struct {
	hooks []fx.Hook
}

func (r *hookRecorder) Append(h fx.Hook) { r.hooks = append(r.hooks, h) }

// ReportWorkerEnabled=false sets nothing up: no call to NATS (the consumer and the tasks stay as they are), no query to
// the database (DB is nil), no worker run with the app.
func TestStartReportWorkerDisabled(t *testing.T) {
	js := &fakeJetStream{}
	lc := &hookRecorder{}
	conf := &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{ReportWorkerEnabled: false}}
	if err := Start(lc, conf, WorkerDeps{NatsJS: js}); err != nil {
		t.Fatal(err)
	}
	if js.calls != 0 || len(lc.hooks) != 0 {
		t.Errorf("the disabled report worker made %d calls to NATS and appended %d hooks, want none", js.calls, len(lc.hooks))
	}
}

// ReportWorkerEnabled=true sets the consumer up and binds a subscription per worker, to be run with the app. It needs
// PENGUIN_TEST_POSTGRES_DSN for the schema check of Start (see process_integration_test.go).
func TestStartReportWorkerEnabled(t *testing.T) {
	tw := newTestWorker(t)
	js := &fakeJetStream{}
	lc := &hookRecorder{}
	conf := &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{ReportWorkerEnabled: true}}
	if err := Start(lc, conf, WorkerDeps{DB: tw.DB, NatsJS: js}); err != nil {
		t.Fatal(err)
	}
	if js.subscribed != defaultConsumerConfig.workers || len(lc.hooks) != 1 {
		t.Errorf("the enabled report worker subscribed %d times and appended %d hooks, want %d and 1",
			js.subscribed, len(lc.hooks), defaultConsumerConfig.workers)
	}
}

// TestReportTasksWaitForAReportWorker checks what the maintenance window of #752 relies on when the report worker is
// turned off: the workers of the old pods stop and their connections close, the durable consumer stays without
// subscribers, and the tasks published meanwhile stay in the work queue until a worker runs again, which handles each
// of them once. It needs PENGUIN_TEST_NATS_URL (see consumer_nats_test.go).
func TestReportTasksWaitForAReportWorker(t *testing.T) {
	s := newTestStream(t)
	var handled handledCounter
	handle := func(ctx context.Context, task *types.ReportTask) error {
		handled.add(task.TaskID)
		return nil
	}

	// a running worker, whose pod is then stopped: its workers stop, its connection closes, the durable stays
	nc, js := s.connect(t)
	w := newWorker(WorkerDeps{NatsJS: js, NatsConn: nc}, s.consumerConfig())
	w.handle = handle
	stop := startConsumer(t, w)
	stop()
	nc.Close()

	// the window: the HTTP API keeps publishing
	const published = 100
	s.publishTasks(t, published)
	time.Sleep(500 * time.Millisecond)
	si, err := s.js.StreamInfo(s.name)
	if err != nil {
		t.Fatal(err)
	}
	if si.State.Msgs != published {
		t.Fatalf("the stream holds %d messages without a worker, want %d", si.State.Msgs, published)
	}
	if ci := s.consumerInfo(t, w.conf.durable); ci.Delivered.Consumer != 0 {
		t.Errorf("%d messages were delivered without a worker, want 0", ci.Delivered.Consumer)
	}

	// the report worker is turned on again
	w2 := newWorker(WorkerDeps{NatsJS: s.js}, s.consumerConfig())
	w2.handle = handle
	startConsumer(t, w2)
	s.waitEmpty(t, w2.conf.durable, 30*time.Second)

	if distinct, total, _ := handled.stats(); distinct != published || total != published {
		t.Errorf("the worker handled %d tasks %d times, want each of %d once", distinct, total, published)
	}
}
