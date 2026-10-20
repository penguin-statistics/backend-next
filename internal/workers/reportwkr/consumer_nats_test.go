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

	"exusiai.dev/backend-next/internal/model/types"
)

// testStream is a work queue stream of a test, like penguin-reports in production.
type testStream struct {
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
	s := &testStream{js: js, name: "TEST_" + id, subject: "T" + id + ".*", publish: "T" + id + ".SINGLE"}
	if _, err := js.AddStream(&nats.StreamConfig{
		Name:      s.name,
		Subjects:  []string{s.subject},
		Retention: nats.WorkQueuePolicy,
		Storage:   nats.FileStorage,
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
