package infra

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
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
