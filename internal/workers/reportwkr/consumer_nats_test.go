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

	"github.com/nats-io/nats.go"
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
