package reportwkr

// Tests of the report consumer against a real NATS server with JetStream. They are skipped unless
// PENGUIN_TEST_NATS_URL points at one, e.g.
//
//	docker run -d --rm -p 127.0.0.1:4222:4222 nats:2.10-alpine -js
//	PENGUIN_TEST_NATS_URL=nats://127.0.0.1:4222 go test ./internal/workers/reportwkr/
//
// Every test works on a stream of its own, which it deletes afterwards. Times are scaled down by 10.

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/pkg/observability"
)

// testStream is a work queue stream of a test, like penguin-reports in production.
type testStream struct {
	url     string
	js      nats.JetStreamContext
	name    string
	subject string // the subject the consumers subscribe to, like REPORT.*
	publish string // a subject of the stream, like REPORT.SINGLE
	// the dead-letter stream of the test, like penguin-reports-dead, and its subject prefix, like REPORT_DEAD.
	dead       string
	deadPrefix string
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
	s := &testStream{
		url: url, js: js, name: "TEST_" + id, subject: "T" + id + ".*", publish: "T" + id + ".SINGLE",
		dead: "TESTDEAD_" + id, deadPrefix: "D" + id + ".",
	}
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
	s.addDeadStream(t)
	return s
}

// addDeadStream creates the dead-letter stream of the test, configured as penguin-reports-dead is.
func (s *testStream) addDeadStream(t *testing.T) {
	t.Helper()
	if _, err := s.js.AddStream(&nats.StreamConfig{
		Name:       s.dead,
		Subjects:   []string{s.deadPrefix + "*"},
		Retention:  nats.LimitsPolicy,
		Storage:    nats.FileStorage,
		Duplicates: 10 * time.Minute,
		MaxAge:     time.Hour,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.js.DeleteStream(s.dead); err != nil && !errors.Is(err, nats.ErrStreamNotFound) {
			t.Error(err)
		}
	})
}

// deadMessages returns the messages of the dead-letter stream of the test, in order.
func (s *testStream) deadMessages(t *testing.T) []*nats.RawStreamMsg {
	t.Helper()
	si, err := s.js.StreamInfo(s.dead)
	if err != nil {
		t.Fatal(err)
	}
	var msgs []*nats.RawStreamMsg
	for seq := si.State.FirstSeq; seq <= si.State.LastSeq && si.State.Msgs > 0; seq++ {
		m, err := s.js.GetMsg(s.dead, seq)
		if err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

// checkDead checks that m is the copy of the message of sequence seq, payload data, in the dead-letter stream, moved
// there after the given number of deliveries with an error containing errPart.
func (s *testStream) checkDead(t *testing.T, m *nats.RawStreamMsg, seq uint64, data []byte, deliveries uint64, errPart string) {
	t.Helper()
	if m.Subject != s.deadPrefix+"SINGLE" || string(m.Data) != string(data) {
		t.Errorf("dead-letter copy of %q under %s, want %q under %sSINGLE", m.Data, m.Subject, data, s.deadPrefix)
	}
	h := m.Header
	if h.Get(HeaderOriginalSubject) != s.publish || h.Get(HeaderOriginalSequence) != strconv.FormatUint(seq, 10) ||
		h.Get(HeaderDeliveries) != strconv.FormatUint(deliveries, 10) || !strings.Contains(h.Get(HeaderLastError), errPart) {
		t.Errorf("dead-letter copy has headers %v, want subject %s, sequence %d, %d deliveries and an error with %q",
			h, s.publish, seq, deliveries, errPart)
	}
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

// countTerminated counts, from now on, the advisories the server sends when it is told never to deliver a message of
// the durable again (Term): an Ack removes the message from the stream too, but sends none.
func (s *testStream) countTerminated(t *testing.T, durable string) (count func() int) {
	t.Helper()
	nc, _ := s.connect(t)
	var n atomic.Int32
	if _, err := nc.Subscribe("$JS.EVENT.ADVISORY.CONSUMER.MSG_TERMINATED."+s.name+"."+durable, func(*nats.Msg) {
		n.Add(1)
	}); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	return func() int { return int(n.Load()) }
}

// logEntries returns the entries of logs logged with the given evt.name.
func logEntries(t *testing.T, logs *lockedBuffer, evtName string) []map[string]any {
	t.Helper()
	logs.mu.Lock()
	defer logs.mu.Unlock()
	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.buf.String()), "\n") {
		if line == "" {
			continue // nothing logged yet
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["evt.name"] == evtName {
			entries = append(entries, entry)
		}
	}
	return entries
}

// consumerConfig is the configuration of the report consumer on the stream, with the times scaled down by 20.
func (s *testStream) consumerConfig() consumerConfig {
	c := defaultConsumerConfig
	c.stream, c.durable, c.legacyDurable, c.subject = s.name, "reports-pull", "reports", s.subject
	c.ackWait = 3 * time.Second
	c.taskTimeout = 500 * time.Millisecond
	c.fetchWait = 100 * time.Millisecond
	c.nakDelay = 750 * time.Millisecond
	c.maxNakDelay = 15 * time.Second
	c.deadSubject = s.deadPrefix
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
// counts returns how many times each key was handled.
func (c *handledCounter) counts() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := make(map[string]int, len(c.times))
	for k, n := range c.times {
		m[k] = n
	}
	return m
}

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

// TestTransientErrorIsNakedAndRetried fails the first delivery of a task as a database error does: the task is
// delivered again after the nak delay, and stored by the second delivery. Each line logged about the task carries it
// whole, ip and accountId included.
func TestTransientErrorIsNakedAndRetried(t *testing.T) {
	s := newTestStream(t)
	tw := newTestWorker(t)
	var logs lockedBuffer
	prev := log.Logger
	log.Logger = zerolog.New(&logs)
	t.Cleanup(func() { log.Logger = prev })
	w := newWorker(tw.WorkerDeps, s.consumerConfig())
	w.NatsJS = s.js
	var mu sync.Mutex
	var calls []time.Time
	w.handle = func(ctx context.Context, task *types.ReportTask) error {
		mu.Lock()
		calls = append(calls, time.Now())
		first := len(calls) == 1
		mu.Unlock()
		if first {
			return errors.New("injected: connection reset by peer")
		}
		return w.process(ctx, task)
	}
	startConsumer(t, w)
	task := newTestTask(t, tw, testAccountID(), time.UnixMicro(1760600000123456), "203.0.113.7", testReport("main_01-07", 30012))
	if _, err := s.js.Publish(s.publish, task); err != nil {
		t.Fatal(err)
	}

	s.waitEmpty(t, w.conf.durable, 10*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("handled %d times, want 2", len(calls))
	}
	if d := calls[1].Sub(calls[0]); d < w.conf.nakDelay {
		t.Errorf("delivered again after %s, want at least the nak delay %s", d, w.conf.nakDelay)
	}
	if ci := s.consumerInfo(t, w.conf.durable); ci.Delivered.Consumer != 2 || ci.NumAckPending != 0 {
		t.Errorf("%d deliveries and %d acks pending, want 2 and 0", ci.Delivered.Consumer, ci.NumAckPending)
	}
	if n := countRows(t, tw.DB, "drop_reports"); n != 1 {
		t.Errorf("%d reports stored, want 1", n)
	}
	var published map[string]any
	if err := json.Unmarshal(task, &published); err != nil {
		t.Fatal(err)
	}
	if failed := logEntries(t, &logs, "reportwkr.failed"); len(failed) != 1 {
		t.Errorf("%d failures logged, want 1", len(failed))
	} else if !reflect.DeepEqual(failed[0]["reportTask"], published) {
		t.Errorf("failure logged with %v, want the task as published, %v", failed[0]["reportTask"], published)
	}
	// the task has one drop and an ip: what process changes in place (merging the drops by item, filling in an empty
	// ip) leaves it as published
	if received := logEntries(t, &logs, "reportwkr.received"); len(received) != 1 {
		t.Errorf("%d tasks logged as received, want 1", len(received))
	} else if !reflect.DeepEqual(received[0]["task"], published) {
		t.Errorf("received task logged as %v, want %v", received[0]["task"], published)
	}
	if processed := logEntries(t, &logs, "reportwkr.processed"); len(processed) != 1 {
		t.Errorf("%d tasks logged as processed, want 1", len(processed))
	} else if e := processed[0]; e["taskId"] != published["taskId"] || !reflect.DeepEqual(e["task"], published) {
		t.Errorf("processed task logged as %v, want taskId %v and the task %v", e, published["taskId"], published)
	}
}

// TestRetryDelay checks the delay before a failed task is delivered again: 15s times its deliveries so far, up to 5
// minutes however often it fails.
func TestRetryDelay(t *testing.T) {
	for _, tc := range []struct {
		delivered uint64
		want      time.Duration
	}{
		{1, 15 * time.Second},
		{5, 75 * time.Second},
		{19, 285 * time.Second},
		{20, 5 * time.Minute},
		{21, 5 * time.Minute},
		{1000, 5 * time.Minute},
	} {
		if got := defaultConsumerConfig.retryDelay(tc.delivered); got != tc.want {
			t.Errorf("after delivery %d: retry in %s, want %s", tc.delivered, got, tc.want)
		}
	}
}

// TestMalformedPayloadIsTerminated publishes a message that is no task: it is moved to the dead-letter stream and
// terminated without being handled, and logged with its sequence number.
func TestMalformedPayloadIsTerminated(t *testing.T) {
	s := newTestStream(t)
	var logs lockedBuffer
	prev := log.Logger
	log.Logger = zerolog.New(&logs)
	t.Cleanup(func() { log.Logger = prev })
	w := newWorker(WorkerDeps{NatsJS: s.js}, s.consumerConfig())
	var handled handledCounter
	w.handle = func(ctx context.Context, task *types.ReportTask) error {
		handled.add(task.TaskID)
		return nil
	}
	terminated := s.countTerminated(t, w.conf.durable)
	deadLettered := deadLetteredCounter("malformed")
	startConsumer(t, w)
	if _, err := s.js.Publish(s.publish, []byte("not-json")); err != nil {
		t.Fatal(err)
	}

	s.waitEmpty(t, w.conf.durable, 10*time.Second)
	// terminated, so not delivered again after the nak delay or AckWait
	time.Sleep(w.conf.ackWait + time.Second)

	if _, total, _ := handled.stats(); total != 0 {
		t.Errorf("handled %d times, want never", total)
	}
	if ci := s.consumerInfo(t, w.conf.durable); ci.Delivered.Consumer != 1 {
		t.Errorf("%d deliveries, want 1", ci.Delivered.Consumer)
	}
	if n := terminated(); n != 1 {
		t.Errorf("%d messages terminated, want 1", n)
	}
	malformed := logEntries(t, &logs, "reportwkr.malformed")
	if len(malformed) != 1 {
		t.Fatalf("%d malformed tasks logged, want 1", len(malformed))
	}
	if e := malformed[0]; e["seq"] != 1.0 || e["subject"] != s.publish || e["data"] != "not-json" || e["level"] != "error" {
		t.Errorf("malformed task logged as %v, want its sequence 1, subject and data at error level", e)
	}
	dead := s.deadMessages(t)
	if len(dead) != 1 {
		t.Fatalf("%d messages in the dead-letter stream, want 1", len(dead))
	}
	s.checkDead(t, dead[0], 1, []byte("not-json"), 1, "invalid character")
	if n := deadLettered(); n != 1 {
		t.Errorf("dead_lettered_total{malformed} increased by %v, want 1", n)
	}
}

// deadLetteredCounter returns how much dead_lettered_total{reason} has increased since it was called.
func deadLetteredCounter(reason string) func() float64 {
	c := observability.ReportDeadLetteredTotal.WithLabelValues(reason)
	before := testutil.ToFloat64(c)
	return func() float64 { return testutil.ToFloat64(c) - before }
}

// TestUnknownStageIsParked processes a task whose second report is of a stage that does not exist: no retry can store
// it, so it is moved to the dead-letter stream, terminated, and logged, all as it was published, although process
// merged the drops of the first report and filled in its empty ip before it failed.
func TestUnknownStageIsParked(t *testing.T) {
	s := newTestStream(t)
	tw := newTestWorker(t)
	var logs lockedBuffer
	prev := log.Logger
	log.Logger = zerolog.New(&logs)
	t.Cleanup(func() { log.Logger = prev })
	w := newWorker(tw.WorkerDeps, s.consumerConfig())
	w.NatsJS = s.js
	terminated := s.countTerminated(t, w.conf.durable)
	deadLettered := deadLetteredCounter("not_found")
	startConsumer(t, w)
	first := testReport("main_01-07", 30012)
	first.Drops = append(first.Drops, &types.Drop{DropType: "EXTRA", ItemID: 30012, Quantity: 1})
	task := newTestTask(t, tw, testAccountID(), time.UnixMicro(1760600000123456), "", first, testReport("no_such_stage", 30012))
	if _, err := s.js.Publish(s.publish, task); err != nil {
		t.Fatal(err)
	}

	s.waitEmpty(t, w.conf.durable, 10*time.Second)
	time.Sleep(2 * w.conf.nakDelay)

	if ci := s.consumerInfo(t, w.conf.durable); ci.Delivered.Consumer != 1 {
		t.Errorf("%d deliveries, want 1", ci.Delivered.Consumer)
	}
	if n := terminated(); n != 1 {
		t.Errorf("%d messages terminated, want 1", n)
	}
	if n := countRows(t, tw.DB, "drop_reports"); n != 0 {
		t.Errorf("%d reports stored, want 0", n)
	}
	parked := logEntries(t, &logs, "reportwkr.parked")
	if len(parked) != 1 {
		t.Fatalf("%d tasks parked, want 1", len(parked))
	}
	var published map[string]any
	if err := json.Unmarshal(task, &published); err != nil {
		t.Fatal(err)
	}
	if e := parked[0]; e["seq"] != 1.0 || !reflect.DeepEqual(e["reportTask"], published) || e["level"] != "error" {
		t.Errorf("task parked as %v, want its sequence 1 and the task as published, %v, at error level", e, published)
	}
	dead := s.deadMessages(t)
	if len(dead) != 1 {
		t.Fatalf("%d messages in the dead-letter stream, want 1", len(dead))
	}
	s.checkDead(t, dead[0], 1, task, 1, "not found")
	if n := deadLettered(); n != 1 {
		t.Errorf("dead_lettered_total{not_found} increased by %v, want 1", n)
	}
}

// TestDeadLetterAfter checks that a task failing at every delivery is retried for over an hour and a half (a database
// outage of about an hour is retried through) and under two hours before it moves to the dead-letter stream.
func TestDeadLetterAfter(t *testing.T) {
	c := defaultConsumerConfig
	var waited time.Duration
	for d := uint64(1); d < c.deadLetterAfter; d++ {
		waited += c.retryDelay(d)
	}
	if waited != 97*time.Minute+30*time.Second {
		t.Errorf("the last delivery comes %s of retry delays after the first, want 97m30s", waited)
	}
}

// TestPermanentFailuresMoveToDeadLetter publishes 128 tasks that fail at every delivery, twice the MaxAckPending
// window that the tasks share, then 20 healthy ones. Each failing task moves to the dead-letter stream at its
// deadLetterAfter-th delivery, so the healthy ones flow past them: each is handled once, and every failing one is in
// the dead-letter stream once, with its subject, sequence, deliveries and last error.
func TestPermanentFailuresMoveToDeadLetter(t *testing.T) {
	s := newTestStream(t)
	var logs lockedBuffer
	prev := log.Logger
	log.Logger = zerolog.New(&logs)
	t.Cleanup(func() { log.Logger = prev })
	conf := s.consumerConfig()
	conf.nakDelay, conf.maxNakDelay = 10*time.Millisecond, 50*time.Millisecond
	conf.deadLetterAfter = 3
	w := newWorker(WorkerDeps{NatsJS: s.js}, conf)
	var handled handledCounter
	w.handle = func(ctx context.Context, task *types.ReportTask) error {
		handled.add(task.TaskID)
		if strings.HasPrefix(task.TaskID, "bad-") {
			return errors.New("injected: value too long for type character varying(32)")
		}
		return nil
	}

	const bad, good = 2 * 64, 20
	deadLettered := deadLetteredCounter("max_deliveries")
	payloads := map[uint64][]byte{}
	for i := 0; i < bad+good; i++ {
		id := fmt.Sprintf("bad-%d", i)
		if i >= bad {
			id = fmt.Sprintf("good-%d", i)
		}
		b, err := json.Marshal(&types.ReportTask{TaskID: id})
		if err != nil {
			t.Fatal(err)
		}
		ack, err := s.js.Publish(s.publish, b)
		if err != nil {
			t.Fatal(err)
		}
		payloads[ack.Sequence] = b
	}
	startConsumer(t, w)
	s.waitEmpty(t, conf.durable, time.Minute)

	counts := handled.counts()
	for i := bad; i < bad+good; i++ {
		if n := counts[fmt.Sprintf("good-%d", i)]; n != 1 {
			t.Errorf("good-%d handled %d times, want 1", i, n)
		}
	}
	for i := 0; i < bad; i++ {
		if n := counts[fmt.Sprintf("bad-%d", i)]; n != int(conf.deadLetterAfter) {
			t.Errorf("bad-%d handled %d times, want %d", i, n, conf.deadLetterAfter)
		}
	}
	dead := s.deadMessages(t)
	if len(dead) != bad {
		t.Fatalf("%d messages in the dead-letter stream, want %d", len(dead), bad)
	}
	for _, m := range dead {
		seq, err := strconv.ParseUint(m.Header.Get(HeaderOriginalSequence), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		s.checkDead(t, m, seq, payloads[seq], conf.deadLetterAfter, "value too long")
	}
	if n := len(logEntries(t, &logs, "reportwkr.dead")); n != bad {
		t.Errorf("%d tasks logged as moved to the dead-letter stream, want %d", n, bad)
	}
	if n := deadLettered(); n != bad {
		t.Errorf("dead_lettered_total{max_deliveries} increased by %v, want %d", n, bad)
	}
}

// TestDeadLetterFailureKeepsTask moves a task to a dead-letter stream that does not exist, and to one whose server
// never answers: the task is not terminated but delivered again, without being handled again, until the stream exists
// and stores it, and the failed publishes leave nothing pending in the JetStream context.
func TestDeadLetterFailureKeepsTask(t *testing.T) {
	for _, tc := range []struct {
		name        string
		breakStream func(t *testing.T, s *testStream) (repair func())
	}{
		{"missing", func(t *testing.T, s *testStream) func() {
			if err := s.js.DeleteStream(s.dead); err != nil {
				t.Fatal(err)
			}
			return func() { s.addDeadStream(t) }
		}},
		{"silent", func(t *testing.T, s *testStream) func() {
			// a subscriber that never answers, as a server whose ack is lost: each publish times out
			if err := s.js.DeleteStream(s.dead); err != nil {
				t.Fatal(err)
			}
			nc, _ := s.connect(t)
			sub, err := nc.SubscribeSync(s.deadPrefix + "*")
			if err != nil {
				t.Fatal(err)
			}
			if err := nc.Flush(); err != nil {
				t.Fatal(err)
			}
			return func() {
				if err := sub.Unsubscribe(); err != nil {
					t.Fatal(err)
				}
				if err := nc.Flush(); err != nil {
					t.Fatal(err)
				}
				s.addDeadStream(t)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStream(t)
			repair := tc.breakStream(t, s)
			var logs lockedBuffer
			prev := log.Logger
			log.Logger = zerolog.New(&logs)
			t.Cleanup(func() { log.Logger = prev })
			conf := s.consumerConfig()
			conf.nakDelay, conf.maxNakDelay = 50*time.Millisecond, 100*time.Millisecond
			conf.taskTimeout = 200 * time.Millisecond
			conf.deadLetterAfter = 1
			w := newWorker(WorkerDeps{NatsJS: s.js}, conf)
			var handled handledCounter
			w.handle = func(ctx context.Context, task *types.ReportTask) error {
				handled.add(task.TaskID)
				return errors.New("injected: permanent")
			}
			terminated := s.countTerminated(t, conf.durable)
			startConsumer(t, w)
			s.publishTasks(t, 1)

			for deadline := time.Now().Add(10 * time.Second); len(logEntries(t, &logs, "reportwkr.dead.failed")) < 3; time.Sleep(20 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the move to the broken dead-letter stream did not fail 3 times")
				}
			}
			if n := terminated(); n != 0 {
				t.Errorf("%d messages terminated while the dead-letter stream is broken, want 0", n)
			}
			si, err := s.js.StreamInfo(s.name)
			if err != nil {
				t.Fatal(err)
			}
			if si.State.Msgs != 1 {
				t.Errorf("stream holds %d messages while the dead-letter stream is broken, want 1", si.State.Msgs)
			}
			if n := s.js.PublishAsyncPending(); n != 0 {
				t.Errorf("%d publishes pending in the JetStream context after the failed moves, want 0", n)
			}

			repair()
			s.waitEmpty(t, conf.durable, 10*time.Second)
			if _, total, _ := handled.stats(); total != 1 {
				t.Errorf("handled %d times, want once: a task past its deliveries is only moved", total)
			}
			if n := terminated(); n != 1 {
				t.Errorf("%d messages terminated, want 1", n)
			}
			dead := s.deadMessages(t)
			if len(dead) != 1 {
				t.Fatalf("%d messages in the dead-letter stream, want 1", len(dead))
			}
			if dead[0].Header.Get(HeaderOriginalSequence) != "1" {
				t.Errorf("dead-letter copy of sequence %s, want 1", dead[0].Header.Get(HeaderOriginalSequence))
			}
			// the shared context still publishes
			if _, err := s.js.Publish(s.publish, []byte("{}")); err != nil {
				t.Errorf("publish after the failed moves: %v", err)
			}
		})
	}
}

// TestNearLimitTaskMovesWithoutHeaders moves a task that the stream accepted 64 bytes under the maximum payload, too
// little room for the headers of its copy: the copy keeps the payload, byte for byte, without the headers, and the move
// is logged.
func TestNearLimitTaskMovesWithoutHeaders(t *testing.T) {
	s := newTestStream(t)
	nc, js := s.connect(t)
	var logs lockedBuffer
	prev := log.Logger
	log.Logger = zerolog.New(&logs)
	t.Cleanup(func() { log.Logger = prev })
	conf := s.consumerConfig()
	conf.deadLetterAfter = 1
	w := newWorker(WorkerDeps{NatsJS: js, NatsConn: nc}, conf)
	w.handle = func(ctx context.Context, task *types.ReportTask) error {
		return errors.New("injected: value too long for type character varying(32)")
	}

	b, err := json.Marshal(&types.ReportTask{TaskID: "near-limit"})
	if err != nil {
		t.Fatal(err)
	}
	b, err = json.Marshal(&types.ReportTask{TaskID: "near-limit", IP: strings.Repeat("x", int(nc.MaxPayload())-64-len(b))})
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(b)) != nc.MaxPayload()-64 {
		t.Fatalf("task of %d bytes, want %d", len(b), nc.MaxPayload()-64)
	}
	if _, err := s.js.Publish(s.publish, b); err != nil {
		t.Fatal(err)
	}
	startConsumer(t, w)
	s.waitEmpty(t, conf.durable, 10*time.Second)

	dead := s.deadMessages(t)
	if len(dead) != 1 {
		t.Fatalf("%d messages in the dead-letter stream, want 1", len(dead))
	}
	if !bytes.Equal(dead[0].Data, b) || len(dead[0].Header) != 0 {
		t.Errorf("dead-letter copy of %d bytes with headers %v, want the %d bytes of the task and no header", len(dead[0].Data), dead[0].Header, len(b))
	}
	if n := len(logEntries(t, &logs, "reportwkr.dead.bare")); n != 1 {
		t.Errorf("%d moves without headers logged, want 1", n)
	}
}
