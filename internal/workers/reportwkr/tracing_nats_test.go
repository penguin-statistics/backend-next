package reportwkr

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/pkg/jetstream"
)

var installTracerProvider sync.Once

// recordSpans returns a recorder of the spans ended during the test, by a tracer provider sampling as production's
// does for a task carrying a trace context: as its parent.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	// set once: the package's tracer delegates to the first provider set
	installTracerProvider.Do(func() {
		otel.SetTracerProvider(tracesdk.NewTracerProvider(tracesdk.WithSampler(tracesdk.ParentBased(tracesdk.AlwaysSample()))))
	})
	provider := otel.GetTracerProvider().(*tracesdk.TracerProvider)
	recorder := tracetest.NewSpanRecorder()
	provider.RegisterSpanProcessor(recorder)
	t.Cleanup(func() { provider.UnregisterSpanProcessor(recorder) })
	return recorder
}

// The consumer span of a task continues the trace of its publisher, whose trace context the task carries in its
// headers (service.Report sets it, from its publish span): it is that span's child, sampled as it is, and the task is
// processed under it. A task without one, as published before the publisher set it, starts a trace of its own.
func TestConsumeContinuesThePublishersTrace(t *testing.T) {
	s := newTestStream(t)
	recorder := recordSpans(t)

	w := newWorker(WorkerDeps{}, s.consumerConfig())
	w.NatsJS = s.js
	var mu sync.Mutex
	handled := map[string]trace.SpanContext{}
	w.handle = func(ctx context.Context, task *types.ReportTask) error {
		mu.Lock()
		defer mu.Unlock()
		handled[task.TaskID] = trace.SpanContextFromContext(ctx)
		return nil
	}
	startConsumer(t, w)

	traceID, _ := trace.TraceIDFromHex("00000000000000004bf92f3577b34da6")
	publishSpanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	publish := func(taskId string, flags trace.TraceFlags) {
		t.Helper()
		data, err := json.Marshal(&types.ReportTask{TaskID: taskId})
		if err != nil {
			t.Fatal(err)
		}
		msg := &nats.Msg{Subject: s.publish, Data: data, Header: nats.Header{}}
		if flags != 0xff {
			publisher := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: publishSpanID, TraceFlags: flags})
			jetstream.InjectTraceContext(trace.ContextWithSpanContext(context.Background(), publisher), msg.Header)
		}
		if _, err := s.js.PublishMsg(msg, nats.MsgId(taskId)); err != nil {
			t.Fatal(err)
		}
	}
	publish("traced", trace.FlagsSampled)
	publish("not-sampled", 0)
	publish("untraced", 0xff)
	s.waitEmpty(t, w.conf.durable, 10*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(handled) != 3 {
		t.Fatalf("handled %v, want the 3 tasks", handled)
	}
	spans := map[string]tracesdk.ReadOnlySpan{}
	for _, span := range recorder.Ended() {
		if span.Name() != "reportwkr.ConsumeTask" {
			continue
		}
		for _, kv := range span.Attributes() {
			if kv.Key == attributeTaskID {
				spans[kv.Value.AsString()] = span
			}
		}
	}

	if span, ok := spans["traced"]; !ok {
		t.Error("no consumer span of the traced task")
	} else {
		if span.SpanContext().TraceID() != traceID || span.Parent().SpanID() != publishSpanID || !span.Parent().IsRemote() {
			t.Errorf("consumer span %v of parent %v, want a child of the publish span %s in trace %s", span.SpanContext(), span.Parent(), publishSpanID, traceID)
		}
		if span.SpanKind() != trace.SpanKindConsumer {
			t.Errorf("consumer span of kind %v, want consumer", span.SpanKind())
		}
		if got := handled["traced"]; got.SpanID() != span.SpanContext().SpanID() {
			t.Errorf("task processed under span %s, want the consumer span %s", got.SpanID(), span.SpanContext().SpanID())
		}
	}
	if _, ok := spans["not-sampled"]; ok {
		t.Error("recorded the consumer span of a task whose publisher did not sample the trace")
	}
	if got := handled["not-sampled"]; got.TraceID() != traceID || got.IsSampled() {
		t.Errorf("not sampled task processed under %v, want the publisher's trace, not sampled", got)
	}
	if span, ok := spans["untraced"]; !ok {
		t.Error("no consumer span of the task published without trace context")
	} else if span.Parent().IsValid() || span.SpanContext().TraceID() == traceID {
		t.Errorf("consumer span of the untraced task has parent %v, want a trace of its own", span.Parent())
	}
}
