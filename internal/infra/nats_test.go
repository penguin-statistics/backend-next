package infra

import (
	"context"
	"math/rand"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
)

// TestEnsureStream runs against a real NATS server with JetStream, and is skipped unless PENGUIN_TEST_NATS_URL points
// at one (see internal/workers/reportwkr/consumer_nats_test.go). It works on streams of its own.
func TestEnsureStream(t *testing.T) {
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
	cfg := &nats.StreamConfig{
		Name:       "TEST_" + id,
		Subjects:   []string{"T" + id + ".*"},
		Retention:  nats.WorkQueuePolicy,
		Discard:    nats.DiscardOld,
		Storage:    nats.FileStorage,
		Replicas:   1,
		Duplicates: time.Minute,
		// a stream left behind by a killed run expires on its own
		MaxAge: time.Hour,
	}
	t.Cleanup(func() { _ = js.DeleteStream(cfg.Name) })

	// created, then left as it is
	for i := 0; i < 2; i++ {
		if err := ensureStream(js, cfg); err != nil {
			t.Fatal(err)
		}
	}

	// updated to a new configuration
	cfg.Discard = nats.DiscardNew
	cfg.Duplicates = 2 * time.Minute
	if err := ensureStream(js, cfg); err != nil {
		t.Fatal(err)
	}
	si, err := js.StreamInfo(cfg.Name)
	if err != nil {
		t.Fatal(err)
	}
	if si.Config.Discard != nats.DiscardNew || si.Config.Duplicates != 2*time.Minute {
		t.Errorf("stream not updated: discard %v, duplicates %s", si.Config.Discard, si.Config.Duplicates)
	}

	// a configuration the server cannot update to is an error
	cfg.Retention = nats.LimitsPolicy
	if err := ensureStream(js, cfg); err == nil {
		t.Error("retention changed from work queue without an error")
	}

	// as is any other error, such as subjects taken by another stream
	other := &nats.StreamConfig{Name: "TEST_" + id + "_OTHER", Subjects: cfg.Subjects, Storage: nats.FileStorage, MaxAge: time.Hour}
	t.Cleanup(func() { _ = js.DeleteStream(other.Name) })
	if err := ensureStream(js, other); err == nil {
		t.Error("stream overlapping another one created without an error")
	}
}

// TestDrainNATS checks that a drain delivers the messages a subscription holds before the connection is closed. It
// is skipped unless PENGUIN_TEST_NATS_URL is set.
func TestDrainNATS(t *testing.T) {
	url := os.Getenv("PENGUIN_TEST_NATS_URL")
	if url == "" {
		t.Skip("PENGUIN_TEST_NATS_URL is not set")
	}
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)

	subject := "T" + strconv.FormatUint(rand.Uint64(), 36)
	var handled atomic.Int32
	if _, err := nc.Subscribe(subject, func(*nats.Msg) {
		time.Sleep(10 * time.Millisecond)
		handled.Add(1)
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if err := nc.Publish(subject, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := drainNATS(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if !nc.IsClosed() {
		t.Error("connection not closed after the drain")
	}
	if n := handled.Load(); n != 20 {
		t.Errorf("%d messages handled before the close, want 20", n)
	}
}

// TestDrainNATSReportsTimeout drains a connection whose subscription is stuck in its handler past the drain timeout:
// the client closes the connection all the same, and drainNATS returns the timeout instead of success, the error still
// reaching the error handler of the connection.
func TestDrainNATSReportsTimeout(t *testing.T) {
	url := os.Getenv("PENGUIN_TEST_NATS_URL")
	if url == "" {
		t.Skip("PENGUIN_TEST_NATS_URL is not set")
	}
	var logged atomic.Int32
	nc, err := nats.Connect(url,
		nats.DrainTimeout(50*time.Millisecond),
		nats.ErrorHandler(func(*nats.Conn, *nats.Subscription, error) { logged.Add(1) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)

	subject := "T" + strconv.FormatUint(rand.Uint64(), 36)
	stuck, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	if _, err := nc.Subscribe(subject, func(*nats.Msg) {
		close(stuck)
		<-release
	}); err != nil {
		t.Fatal(err)
	}
	if err := nc.Publish(subject, nil); err != nil {
		t.Fatal(err)
	}
	<-stuck

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = drainNATS(ctx, nc)
	if !errors.Is(err, nats.ErrDrainTimeout) {
		t.Errorf("drainNATS returned %v, want %v", err, nats.ErrDrainTimeout)
	}
	if !nc.IsClosed() {
		t.Error("connection not closed after the drain")
	}
	if logged.Load() == 0 {
		t.Error("the drain timeout did not reach the error handler of the connection")
	}
}

// keepDialer dials TCP and keeps the last connection it made, so a test can break it.
type keepDialer struct {
	mu   sync.Mutex
	conn net.Conn
}

func (d *keepDialer) Dial(network, address string) (net.Conn, error) {
	c, err := net.Dial(network, address)
	d.mu.Lock()
	d.conn = c
	d.mu.Unlock()
	return c, err
}

// TestDrainNATSReportsLostConnection breaks the TCP connection while a subscription holds the drain: the client closes
// the connection without calling the error handler, and drainNATS returns the transport error instead of success. A
// connection that had an error before the drain still drains with success (TestDrainNATSIgnoresEarlierError).
func TestDrainNATSReportsLostConnection(t *testing.T) {
	url := os.Getenv("PENGUIN_TEST_NATS_URL")
	if url == "" {
		t.Skip("PENGUIN_TEST_NATS_URL is not set")
	}
	d := &keepDialer{}
	nc, err := nats.Connect(url, nats.SetCustomDialer(d), nats.ErrorHandler(func(*nats.Conn, *nats.Subscription, error) {}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)

	subject := "T" + strconv.FormatUint(rand.Uint64(), 36)
	stuck, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	var once sync.Once
	if _, err := nc.Subscribe(subject, func(*nats.Msg) {
		once.Do(func() { close(stuck) })
		<-release
	}); err != nil {
		t.Fatal(err)
	}
	if err := nc.Publish(subject, nil); err != nil {
		t.Fatal(err)
	}
	<-stuck

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- drainNATS(ctx, nc) }()
	for deadline := time.Now().Add(5 * time.Second); !nc.IsDraining(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the connection did not start draining")
		}
	}
	d.mu.Lock()
	if err := d.conn.Close(); err != nil {
		t.Fatal(err)
	}
	d.mu.Unlock()

	if err := <-done; err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("drainNATS returned %v, want the error of the lost connection", err)
	}
	if !nc.IsClosed() {
		t.Error("connection not closed after the drain")
	}
}

// TestDrainNATSIgnoresEarlierError drains a connection whose last error is from before the drain (a slow consumer,
// which keeps the connection): the drain succeeds, and drainNATS returns nil.
func TestDrainNATSIgnoresEarlierError(t *testing.T) {
	url := os.Getenv("PENGUIN_TEST_NATS_URL")
	if url == "" {
		t.Skip("PENGUIN_TEST_NATS_URL is not set")
	}
	slow := make(chan struct{}, 1)
	nc, err := nats.Connect(url, nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
		if errors.Is(err, nats.ErrSlowConsumer) {
			select {
			case slow <- struct{}{}:
			default:
			}
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)

	subject := "T" + strconv.FormatUint(rand.Uint64(), 36)
	release := make(chan struct{})
	sub, err := nc.Subscribe(subject, func(*nats.Msg) { <-release })
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.SetPendingLimits(1, -1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := nc.Publish(subject, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := nc.LastError(); !errors.Is(err, nats.ErrSlowConsumer) {
		t.Fatalf("last error %v before the drain, want %v", err, nats.ErrSlowConsumer)
	}
	// the error handler has run, so drainNATS does not replace it while the client calls it
	<-slow
	close(release)
	if err := sub.Unsubscribe(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := drainNATS(ctx, nc); err != nil {
		t.Errorf("drainNATS returned %v, want nil: the last error is from before the drain", err)
	}
	if !nc.IsClosed() {
		t.Error("connection not closed after the drain")
	}
}
