package infra

import (
	"bytes"
	"context"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// TestNatsErrorHandlerWithoutSubscription checks that an error of the connection itself, which comes without a
// subscription, is logged instead of panicking the process.
func TestNatsErrorHandlerWithoutSubscription(t *testing.T) {
	var b bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&b)
	t.Cleanup(func() { log.Logger = prev })

	natsErrorHandler(nil, nil, nats.ErrSlowConsumer)
	natsErrorHandler(nil, &nats.Subscription{Subject: "REPORT.*"}, nats.ErrSlowConsumer)

	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("logged %q, want 2 entries", lines)
	}
	if strings.Contains(lines[0], "sub.subject") {
		t.Errorf("connection error logged with a subject: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"sub.subject":"REPORT.*"`) {
		t.Errorf("subscription error logged without its subject: %s", lines[1])
	}
}

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
