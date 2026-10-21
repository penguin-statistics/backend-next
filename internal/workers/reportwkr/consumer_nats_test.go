package reportwkr

// Tests of the report consumer against a real NATS server with JetStream. They are skipped unless
// PENGUIN_TEST_NATS_URL points at one, e.g.
//
//	docker run -d --rm -p 127.0.0.1:4222:4222 nats:2.10-alpine -js
//	PENGUIN_TEST_NATS_URL=nats://127.0.0.1:4222 go test ./internal/workers/reportwkr/
//
// Every test works on a stream of its own, which it deletes afterwards. Times are scaled down by 10.

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"

	"exusiai.dev/backend-next/internal/model/types"
)

// testStream is a work queue stream of a test, like penguin-reports in production.
type testStream struct {
	url     string
	js      nats.JetStreamContext
	name    string
	subject string // the subject the consumers subscribe to, like REPORT.*
	publish string // a subject of the stream, like REPORT.SINGLE
}

// newTestStream connects to PENGUIN_TEST_NATS_URL and creates a stream of its own.
func newTestStream(t *testing.T) *testStream {
	t.Helper()
	url := os.Getenv("PENGUIN_TEST_NATS_URL")
	if url == "" {
		t.Skip("PENGUIN_TEST_NATS_URL is not set")
	}
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}

	id := strconv.FormatUint(rand.Uint64(), 36)
	s := &testStream{url: url, js: js, name: "TEST_" + id, subject: "T" + id + ".*", publish: "T" + id + ".SINGLE"}
	if _, err := js.AddStream(&nats.StreamConfig{
		Name:      s.name,
		Subjects:  []string{s.subject},
		Retention: nats.WorkQueuePolicy,
		Storage:   nats.FileStorage,
		// a stream left behind by a killed run expires on its own
		MaxAge: time.Hour,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := js.DeleteStream(s.name); err != nil {
			t.Error(err)
		}
	})
	return s
}

// connect returns a connection of its own to the server of the stream, closed after the test.
func (s *testStream) connect(t *testing.T) (*nats.Conn, nats.JetStreamContext) {
	t.Helper()
	nc, err := nats.Connect(s.url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	return nc, js
}

// publishTasks publishes n tasks of no report, with the task ids task-0 to task-<n-1>.
func (s *testStream) publishTasks(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		b, err := json.Marshal(&types.ReportTask{TaskID: fmt.Sprintf("task-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.js.Publish(s.publish, b); err != nil {
			t.Fatal(err)
		}
	}
}

// consumerInfo returns the info of the durable, which must exist.
func (s *testStream) consumerInfo(t *testing.T, durable string) *nats.ConsumerInfo {
	t.Helper()
	ci, err := s.js.ConsumerInfo(s.name, durable)
	if err != nil {
		t.Fatal(err)
	}
	return ci
}

// consumerConfig is the configuration of the report consumer on the stream, with the times scaled down by 20.
func (s *testStream) consumerConfig() consumerConfig {
	c := defaultConsumerConfig
	c.stream, c.durable, c.legacyDurable, c.subject = s.name, "reports-pull", "reports", s.subject
	c.ackWait = 3 * time.Second
	c.taskTimeout = 500 * time.Millisecond
	c.fetchWait = 100 * time.Millisecond
	return c
}

// startConsumer sets the consumer of w up and starts its workers. The returned function stops them and waits for the
// tasks in progress; it is called at the end of the test too.
func startConsumer(t *testing.T, w *Worker) (stop func()) {
	t.Helper()
	if err := w.subscribe(); err != nil {
		t.Fatal(err)
	}
	w.start()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := w.stop(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// waitEmpty waits until the stream holds no message, that is until every message was acked, and returns the
// highest number of redelivered messages the durable reported meanwhile (acked messages no longer count there).
func (s *testStream) waitEmpty(t *testing.T, durable string, timeout time.Duration) (maxRedelivered int) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if ci, err := s.js.ConsumerInfo(s.name, durable); err == nil && ci.NumRedelivered > maxRedelivered {
			maxRedelivered = ci.NumRedelivered
		}
		si, err := s.js.StreamInfo(s.name)
		if err != nil {
			t.Fatal(err)
		}
		if si.State.Msgs == 0 {
			return maxRedelivered
		}
		if time.Now().After(deadline) {
			t.Fatalf("stream still holds %d messages after %s", si.State.Msgs, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Legacy consumer: a copy of the push consumer of reportwkr.go (Start, Worker.Consumer, Worker.ingestPreprocess), the
// shape that handled tasks several times on 2026-10-16, with the times scaled down by 10. It is kept here as the
// reproduction of the incident when that consumer is replaced.
const (
	legacyWorkers       = 4 // runtime.NumCPU() of the production node
	legacyAckWait       = time.Second
	legacyMaxAckPending = 128
	legacyTaskTimeout   = time.Second
	legacyInProgress    = 500 * time.Millisecond
)

// startLegacyConsumer subscribes the legacy consumer to the stream under the durable name durable and runs handle for
// each message it receives. The returned stop function stops the workers, waits for the messages in progress and
// unsubscribes; the messages still buffered are left as they are.
func startLegacyConsumer(t *testing.T, s *testStream, durable string, handle func(ctx context.Context, msg *nats.Msg) error) (stop func()) {
	t.Helper()
	done := make(chan struct{})
	var wg sync.WaitGroup
	var subs []*nats.Subscription
	for i := 0; i < legacyWorkers; i++ {
		msgChan := make(chan *nats.Msg, 512)
		sub, err := s.js.ChanQueueSubscribe(s.subject, durable, msgChan, nats.AckWait(legacyAckWait), nats.MaxAckPending(legacyMaxAckPending))
		if err != nil {
			t.Fatal(err)
		}
		subs = append(subs, sub)

		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case msg := <-msgChan:
					legacyIngest(msg, handle)
				case <-done:
					return
				}
			}
		}()
	}

	var once sync.Once
	stop = func() {
		once.Do(func() {
			close(done)
			wg.Wait()
			for _, sub := range subs {
				_ = sub.Unsubscribe()
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// legacyIngest acks the message whatever the outcome, as the legacy ingestPreprocess did.
func legacyIngest(msg *nats.Msg, handle func(ctx context.Context, msg *nats.Msg) error) {
	defer func() {
		_ = msg.Ack()
	}()

	taskCtx, cancelTask := context.WithTimeout(context.Background(), legacyTaskTimeout)
	defer cancelTask()

	inprogressInformer := time.AfterFunc(legacyInProgress, func() {
		_ = msg.InProgress()
	})
	defer inprogressInformer.Stop()

	_ = handle(taskCtx, msg)
}

// handledCounter counts how many times each message payload was handled.
type handledCounter struct {
	mu    sync.Mutex
	times map[string]int
}

func (c *handledCounter) add(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.times == nil {
		c.times = map[string]int{}
	}
	c.times[key]++
}

// stats returns the number of distinct keys handled, the number of handlings and the keys handled more than once.
func (c *handledCounter) stats() (distinct, total, repeated int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range c.times {
		total += n
		if n > 1 {
			repeated++
		}
	}
	return len(c.times), total, repeated
}

// TestLegacyPushConsumerRedeliversQueuedMessages reproduces the duplicate processing of 2026-10-16: the push consumer
// lets the server deliver up to MaxAckPending messages, which wait in the client's channels behind a slow handler.
// The AckWait of a message runs from its delivery, not from the start of its handling, so with ~32 messages queued
// per worker and 250ms each (the slow database), most expire before they are handled and are delivered again, to
// be handled again.
func TestLegacyPushConsumerRedeliversQueuedMessages(t *testing.T) {
	s := newTestStream(t)
	const published = 128

	var handled handledCounter
	stop := startLegacyConsumer(t, s, "legacy", func(ctx context.Context, msg *nats.Msg) error {
		time.Sleep(250 * time.Millisecond)
		handled.add(string(msg.Data))
		return nil
	})

	for i := 0; i < published; i++ {
		if _, err := s.js.Publish(s.publish, []byte(fmt.Sprintf("task-%d", i))); err != nil {
			t.Fatal(err)
		}
	}

	maxRedelivered := s.waitEmpty(t, "legacy", time.Minute)
	// copies delivered before the original was acked are still queued: give the workers a moment to reach some
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, _, repeated := handled.stats(); repeated > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()

	distinct, total, repeated := handled.stats()
	t.Logf("%d messages published, %d handled %d times, %d of them more than once; up to %d redelivered at once",
		published, distinct, total, repeated, maxRedelivered)
	if distinct != published {
		t.Errorf("%d distinct messages handled, want %d", distinct, published)
	}
	if repeated == 0 {
		t.Error("no message was handled more than once; the legacy consumer no longer reproduces the incident")
	}
	if maxRedelivered == 0 {
		t.Error("the server redelivered no message")
	}
}

// slowReportInserts makes every insert into drop_reports take 250ms, inside the transaction and so holding the lock
// of the task: the slow database of 2026-10-16.
const slowReportInserts = `
CREATE FUNCTION slow_report_insert() RETURNS trigger AS $$
BEGIN
	PERFORM pg_sleep(0.25);
	RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER slow_report_insert BEFORE INSERT ON drop_reports FOR EACH ROW EXECUTE FUNCTION slow_report_insert();
`

// TestLegacyStormWithIdempotentProcess runs the reproduction of TestLegacyPushConsumerRedeliversQueuedMessages with
// process as the handler, on PostgreSQL: the copies are processed again, but each task is stored once.
func TestLegacyStormWithIdempotentProcess(t *testing.T) {
	s := newTestStream(t)
	w := newTestWorker(t)
	// a task stored beforehand creates the pattern, as in production almost every pattern exists: tasks of different
	// accounts creating a pattern at once conflict on its hash, and the legacy consumer drops the losers
	if err := processTask(t, w, newTestTask(t, w, testAccountID(), time.UnixMicro(1760600000000000), "203.0.113.7", testReport("main_01-07", 30012))); err != nil {
		t.Fatal(err)
	}
	if _, err := w.DB.ExecContext(context.Background(), slowReportInserts); err != nil {
		t.Fatal(err)
	}
	duplicates := captureLogs(t)
	const published = 128

	var handled handledCounter
	var failed handledCounter
	stop := startLegacyConsumer(t, s, "legacy", func(ctx context.Context, msg *nats.Msg) error {
		task := &types.ReportTask{}
		if err := json.Unmarshal(msg.Data, task); err != nil {
			return err
		}
		if err := w.process(ctx, task); err != nil {
			failed.add(err.Error())
			return err
		}
		handled.add(task.TaskID)
		return nil
	})

	// 4 tasks per account in a row, as an account's backlog is: they are handled at once by the 4 workers
	accountId := testAccountID()
	createdAt := time.UnixMicro(1760600000123456)
	for i := 0; i < published; i++ {
		task := newTestTask(t, w, accountId+i/4, createdAt.Add(time.Duration(i)*time.Millisecond), "203.0.113.7", testReport("main_01-07", 30012))
		if _, err := s.js.Publish(s.publish, task); err != nil {
			t.Fatal(err)
		}
	}

	s.waitEmpty(t, "legacy", time.Minute)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, _, repeated := handled.stats(); repeated > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()

	distinct, total, repeated := handled.stats()
	t.Logf("%d tasks published, %d handled %d times, %d of them more than once", published, distinct, total, repeated)
	if _, errs, _ := failed.stats(); errs > 0 {
		t.Errorf("%d handlings failed: %v", errs, failed.times)
	}
	if repeated == 0 {
		t.Fatal("no task was handled more than once; the test proves nothing")
	}
	if distinct != published {
		t.Errorf("%d distinct tasks handled, want %d", distinct, published)
	}
	if n := duplicates("reportwkr.duplicate"); n != total-distinct {
		t.Errorf("%d copies skipped, want %d", n, total-distinct)
	}

	var reports, tasks int
	if err := w.DB.NewRaw("SELECT count(*), count(DISTINCT (account_id, created_at)) FROM drop_reports WHERE created_at >= ?", createdAt).
		Scan(context.Background(), &reports, &tasks); err != nil {
		t.Fatal(err)
	}
	if reports != published || tasks != published {
		t.Errorf("%d reports of %d tasks stored, want %d of %d", reports, tasks, published, published)
	}
	if n := countRows(t, w.DB, "drop_report_extras"); n != published+1 {
		t.Errorf("%d extras stored, want %d", n, published+1)
	}
}

// TestCommitThenCrashBeforeAck processes a task and stops before acking it, as a process dying between the commit
// and the Ack: the server delivers the message again after AckWait, and that copy is skipped and acked.
func TestCommitThenCrashBeforeAck(t *testing.T) {
	s := newTestStream(t)
	w := newTestWorker(t)
	duplicates := captureLogs(t)

	// the consumer is reduced to fetching and acking by hand, to stop between the commit and the Ack
	sub, err := s.js.PullSubscribe(s.subject, "crash", nats.AckWait(500*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	fetch := func() *nats.Msg {
		t.Helper()
		msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		return msgs[0]
	}

	task := newTestTask(t, w, testAccountID(), time.UnixMicro(1760600000123456), "203.0.113.7", testReport("main_01-07", 30012))
	if _, err := s.js.Publish(s.publish, task); err != nil {
		t.Fatal(err)
	}

	if err := processTask(t, w, fetch().Data); err != nil {
		t.Fatal(err)
	}
	// the process dies here: no Ack, no Nak

	msg := fetch()
	if md, err := msg.Metadata(); err != nil {
		t.Fatal(err)
	} else if md.NumDelivered != 2 {
		t.Errorf("delivered %d times, want 2", md.NumDelivered)
	}
	if err := processTask(t, w, msg.Data); err != nil {
		t.Fatal(err)
	}
	if err := msg.AckSync(); err != nil {
		t.Fatal(err)
	}

	s.waitEmpty(t, "crash", 5*time.Second)
	if n := duplicates("reportwkr.duplicate"); n != 1 {
		t.Errorf("%d copies skipped, want 1", n)
	}
	if n := countRows(t, w.DB, "drop_reports"); n != 1 {
		t.Errorf("%d reports stored, want 1", n)
	}
	if n := countRows(t, w.DB, "drop_report_extras"); n != 1 {
		t.Errorf("%d extras stored, want 1", n)
	}
}

// TestPullConsumerNoRedeliveryUnderSlowHandler runs the load of TestLegacyPushConsumerRedeliversQueuedMessages on the
// pull consumer: a worker asks for a task only when it is free, so no task waits in the client while its AckWait runs,
// and every task is delivered and handled once.
func TestPullConsumerNoRedeliveryUnderSlowHandler(t *testing.T) {
	s := newTestStream(t)
	const published = 128

	var handled handledCounter
	w := newWorker(WorkerDeps{NatsJS: s.js}, s.consumerConfig())
	w.handle = func(ctx context.Context, task *types.ReportTask) error {
		time.Sleep(250 * time.Millisecond)
		handled.add(task.TaskID)
		return nil
	}
	startConsumer(t, w)
	s.publishTasks(t, published)

	maxRedelivered := s.waitEmpty(t, w.conf.durable, time.Minute)

	distinct, total, repeated := handled.stats()
	t.Logf("%d tasks published, %d handled %d times", published, distinct, total)
	if distinct != published || total != published || repeated != 0 {
		t.Errorf("%d tasks handled %d times, %d of them more than once; want each of %d once", distinct, total, repeated, published)
	}
	if maxRedelivered != 0 {
		t.Errorf("up to %d tasks redelivered at once, want none", maxRedelivered)
	}
	if ci := s.consumerInfo(t, w.conf.durable); ci.Delivered.Consumer != published {
		t.Errorf("%d deliveries, want %d", ci.Delivered.Consumer, published)
	}
}

// TestStartReconcilesOrMigratesConsumer checks the consumer Start sets up: a missing one is created, a pull consumer of
// another configuration is updated, and the push consumer of the previous versions is deleted.
func TestStartReconcilesOrMigratesConsumer(t *testing.T) {
	t.Run("created", func(t *testing.T) {
		s := newTestStream(t)
		w := newWorker(WorkerDeps{NatsJS: s.js}, s.consumerConfig())
		if err := w.subscribe(); err != nil {
			t.Fatal(err)
		}

		cfg := s.consumerInfo(t, w.conf.durable).Config
		if cfg.DeliverSubject != "" || cfg.AckPolicy != nats.AckExplicitPolicy || cfg.DeliverPolicy != nats.DeliverAllPolicy ||
			cfg.AckWait != w.conf.ackWait || cfg.MaxAckPending != w.conf.maxAckPending || cfg.FilterSubject != s.subject {
			t.Errorf("consumer created with %+v", cfg)
		}
		if len(w.subs) != w.conf.workers {
			t.Errorf("%d subscriptions, want %d", len(w.subs), w.conf.workers)
		}
	})

	t.Run("updated", func(t *testing.T) {
		s := newTestStream(t)
		w := newWorker(WorkerDeps{NatsJS: s.js}, s.consumerConfig())
		if _, err := s.js.AddConsumer(s.name, &nats.ConsumerConfig{
			Durable:       w.conf.durable,
			AckPolicy:     nats.AckExplicitPolicy,
			AckWait:       10 * time.Second,
			MaxAckPending: 1000,
			FilterSubject: s.subject,
		}); err != nil {
			t.Fatal(err)
		}
		if err := w.subscribe(); err != nil {
			t.Fatal(err)
		}

		cfg := s.consumerInfo(t, w.conf.durable).Config
		if cfg.AckWait != w.conf.ackWait || cfg.MaxAckPending != w.conf.maxAckPending {
			t.Errorf("consumer has AckWait %s and MaxAckPending %d, want %s and %d", cfg.AckWait, cfg.MaxAckPending, w.conf.ackWait, w.conf.maxAckPending)
		}
	})

	t.Run("push", func(t *testing.T) {
		s := newTestStream(t)
		w := newWorker(WorkerDeps{NatsJS: s.js}, s.consumerConfig())
		// as the legacy ChanQueueSubscribe creates it
		if _, err := s.js.AddConsumer(s.name, &nats.ConsumerConfig{
			Durable:        w.conf.legacyDurable,
			DeliverSubject: nats.NewInbox(),
			DeliverGroup:   w.conf.legacyDurable,
			AckPolicy:      nats.AckExplicitPolicy,
			AckWait:        legacyAckWait,
			MaxAckPending:  legacyMaxAckPending,
			FilterSubject:  s.subject,
		}); err != nil {
			t.Fatal(err)
		}
		migrated := captureLogs(t)

		if err := w.subscribe(); err != nil {
			t.Fatal(err)
		}

		if _, err := s.js.ConsumerInfo(s.name, w.conf.legacyDurable); !errors.Is(err, nats.ErrConsumerNotFound) {
			t.Errorf("push consumer still there: %v", err)
		}
		cfg := s.consumerInfo(t, w.conf.durable).Config
		if cfg.DeliverSubject != "" || cfg.AckWait != w.conf.ackWait || cfg.MaxAckPending != w.conf.maxAckPending {
			t.Errorf("consumer created with %+v", cfg)
		}
		if len(w.subs) != w.conf.workers {
			t.Errorf("%d subscriptions, want %d", len(w.subs), w.conf.workers)
		}
		if n := migrated("reportwkr.consumer.migrate"); n != 1 {
			t.Errorf("%d migrations logged, want 1", n)
		}
	})
}

// TestBindRequiresDurable deletes the durable between its setup and the subscriptions, as a second pod starting at once
// could: the subscriptions fail, rather than create a durable of the library's defaults that a drain would delete.
func TestBindRequiresDurable(t *testing.T) {
	s := newTestStream(t)
	w := newWorker(WorkerDeps{NatsJS: s.js}, s.consumerConfig())
	if err := w.setUpConsumer(); err != nil {
		t.Fatal(err)
	}
	if err := s.js.DeleteConsumer(s.name, w.conf.durable); err != nil {
		t.Fatal(err)
	}

	if err := w.bind(); err == nil {
		t.Error("subscribed without the durable")
	}
	if _, err := s.js.ConsumerInfo(s.name, w.conf.durable); !errors.Is(err, nats.ErrConsumerNotFound) {
		t.Errorf("durable created again: %v", err)
	}
}

// TestStopFlushesAcksAndKeepsDurable stops the consumer with a task in progress: the stop waits for the task and its
// Ack, the workers fetch nothing more, and draining the connection afterwards, as the app does, keeps the durable.
func TestStopFlushesAcksAndKeepsDurable(t *testing.T) {
	s := newTestStream(t)
	nc, js := s.connect(t)
	w := newWorker(WorkerDeps{NatsJS: js}, s.consumerConfig())
	started, release := make(chan struct{}), make(chan struct{})
	w.handle = func(ctx context.Context, task *types.ReportTask) error {
		close(started)
		<-release
		return nil
	}
	stop := startConsumer(t, w)
	s.publishTasks(t, 1)
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("task not handled")
	}

	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("stopped with a task in progress")
	case <-time.After(500 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("not stopped after the task in progress")
	}

	if err := nc.Drain(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); !nc.IsClosed(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("connection not drained")
		}
	}

	// a task published after the stop stays queued, and the task handled is not delivered again after AckWait
	s.publishTasks(t, 1)
	time.Sleep(w.conf.ackWait + time.Second)
	ci := s.consumerInfo(t, w.conf.durable)
	if ci.NumAckPending != 0 || ci.Delivered.Consumer != 1 || ci.NumPending != 1 {
		t.Errorf("after the stop: %d acks pending, %d deliveries, %d tasks pending; want 0, 1, 1",
			ci.NumAckPending, ci.Delivered.Consumer, ci.NumPending)
	}
}
