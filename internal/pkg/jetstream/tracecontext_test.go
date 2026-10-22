package jetstream

import (
	"context"
	"testing"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/trace"
)

func testSpanContext(t *testing.T, flags trace.TraceFlags) trace.SpanContext {
	t.Helper()
	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	state, err := trace.ParseTraceState("dd=s:1;o:rum")
	if err != nil {
		t.Fatal(err)
	}
	return trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: flags, TraceState: state})
}

// The trace context of the publisher's span travels in the lower-case W3C headers, next to the headers NATS sets
// (Nats-Msg-Id, which deduplicates a task published twice, is left as is), and the consumer reads it back as the remote
// parent of its span, sampled or not as the publisher's.
func TestTraceContextRoundTrip(t *testing.T) {
	for _, flags := range []trace.TraceFlags{trace.FlagsSampled, 0} {
		sc := testSpanContext(t, flags)
		h := nats.Header{}
		h.Set(nats.MsgIdHdr, "key1")
		InjectTraceContext(trace.ContextWithSpanContext(context.Background(), sc), h)

		want := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-" + flags.String()
		if got := h.Get("traceparent"); got != want {
			t.Errorf("traceparent = %q, want %q", got, want)
		}
		if got := h.Get("tracestate"); got != "dd=s:1;o:rum" {
			t.Errorf("tracestate = %q, want the span's", got)
		}
		if got := h.Get(nats.MsgIdHdr); got != "key1" {
			t.Errorf("%s = %q, want it kept", nats.MsgIdHdr, got)
		}

		got := trace.SpanContextFromContext(ExtractTraceContext(context.Background(), h))
		if !got.IsRemote() || !got.Equal(sc.WithRemote(true)) {
			t.Errorf("extracted %+v, want the publisher's %+v as a remote parent", got, sc)
		}
	}
}

func TestTraceContextAbsent(t *testing.T) {
	// a context without a span sets nothing
	h := nats.Header{}
	InjectTraceContext(context.Background(), h)
	if len(h) != 0 {
		t.Errorf("headers %v set from a context without a span, want none", h)
	}

	// a message without headers, or without a valid traceparent, has no parent
	for _, h := range []nats.Header{nil, {}, {nats.MsgIdHdr: {"key1"}}, {"traceparent": {"00-not-a-trace-01"}}, {"Traceparent": {"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}}} {
		ctx := context.Background()
		if got := ExtractTraceContext(ctx, h); trace.SpanContextFromContext(got).IsValid() {
			t.Errorf("extracted a parent from %v, want none", h)
		}
	}
}

func BenchmarkTraceContext(b *testing.B) {
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	b.Run("inject", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			InjectTraceContext(ctx, nats.Header{})
		}
	})
	b.Run("extract", func(b *testing.B) {
		h := nats.Header{}
		InjectTraceContext(ctx, h)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			ExtractTraceContext(context.Background(), h)
		}
	})
}
