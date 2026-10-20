package service

import (
	"net"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/nats-io/nats.go"
)

// reconnectingNATS returns a connection to an address nothing listens on. With RetryOnFailedConnect it keeps
// reconnecting in the background, as while the NATS pod moves to another node.
func reconnectingNATS(t *testing.T) *nats.Conn {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	nc, err := nats.Connect("nats://"+addr, nats.RetryOnFailedConnect(true))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	if nc.Status() != nats.RECONNECTING {
		t.Fatalf("nats status is %s, want RECONNECTING", nc.Status())
	}
	return nc
}

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
