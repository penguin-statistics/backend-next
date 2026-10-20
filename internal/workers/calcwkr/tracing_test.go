package calcwkr

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-redsync/redsync/v4"
	redsyncredis "github.com/go-redsync/redsync/v4/redis"
	"go.opentelemetry.io/otel/codes"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// lockedPool is a redsync pool whose mutexes can always be acquired and extended.
type lockedPool struct{}

func (lockedPool) Get(context.Context) (redsyncredis.Conn, error) { return lockedConn{}, nil }

type lockedConn struct{}

func (lockedConn) Get(string) (string, error)                        { return "", nil }
func (lockedConn) Set(string, string) (bool, error)                  { return true, nil }
func (lockedConn) SetNX(string, string, time.Duration) (bool, error) { return true, nil }
func (lockedConn) Eval(*redsyncredis.Script, ...interface{}) (interface{}, error) {
	return int64(1), nil
}
func (lockedConn) PTTL(string) (time.Duration, error) { return time.Minute, nil }
func (lockedConn) Close() error                       { return nil }

func TestMicrotaskTracesItsWorkUnderTheCallersSpan(t *testing.T) {
	// the package tracer delegates to the first global provider only, so swap the tracer itself
	recorder := tracetest.NewSpanRecorder()
	provider := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(recorder))
	previous := tracer
	tracer = provider.Tracer("calcwkr")
	t.Cleanup(func() { tracer = previous })

	w := &Worker{syncMutex: redsync.New(lockedPool{}).NewMutex("mutex:calcwkr:test")}
	ctx, parent := provider.Tracer("test").Start(context.Background(), "calcwkr.server")

	var jobSpan trace.SpanContext
	if err := w.microtask(ctx, "dropMatrix", "CN", func(ctx context.Context) error {
		// what the job's database queries (bunotel) would be children of
		jobSpan = trace.SpanContextFromContext(ctx)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	errFailed := errors.New("failed")
	if err := w.microtask(ctx, "siteStats", "CN", func(ctx context.Context) error { return errFailed }); !errors.Is(err, errFailed) {
		t.Fatalf("got error %v, want %v", err, errFailed)
	}
	parent.End()

	spans := recorder.Ended()
	if len(spans) != 3 {
		t.Fatalf("got %d spans, want 3", len(spans))
	}
	dropMatrix, siteStats := spans[0], spans[1]
	if dropMatrix.Name() != "calcwkr.dropMatrix" || siteStats.Name() != "calcwkr.siteStats" {
		t.Fatalf("got spans %q, %q", dropMatrix.Name(), siteStats.Name())
	}
	for _, span := range []tracesdk.ReadOnlySpan{dropMatrix, siteStats} {
		if span.Parent().SpanID() != parent.SpanContext().SpanID() {
			t.Errorf("%s is not a child of the server span", span.Name())
		}
	}
	if jobSpan.SpanID() != dropMatrix.SpanContext().SpanID() {
		t.Error("the job did not get the microtask span in its context")
	}
	if dropMatrix.Status().Code != codes.Ok || siteStats.Status().Code != codes.Error || len(siteStats.Events()) == 0 {
		t.Errorf("got statuses %v and %v (%d events), want ok and a recorded error",
			dropMatrix.Status(), siteStats.Status(), len(siteStats.Events()))
	}
}
