package infra

import (
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
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
