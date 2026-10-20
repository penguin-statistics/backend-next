package service

import (
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Before this check, the publish was buffered by the client: the request waited 10 seconds for ErrNatsTimeout and
// the buffered report was still sent once NATS came back.
func TestCommitReportTaskFailsFastWhileNATSIsReconnecting(t *testing.T) {
	nc := reconnectingNATS(t)
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}

	// built through NewReport so that a dropped NatsConn wiring fails here instead of panicking in production
	s := NewReport(nil, nil, nc, js, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	start := time.Now()
	_, err = commitInRequest(t, s, "REPORT.SINGLE", nil)
	if err != ErrReportQueueUnavailable {
		t.Fatalf("got %v, want ErrReportQueueUnavailable", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("took %s to fail, want it to fail right away", took)
	}
	if ErrReportQueueUnavailable.StatusCode != fiber.StatusServiceUnavailable {
		t.Fatalf("status code is %d, want 503", ErrReportQueueUnavailable.StatusCode)
	}
}
