package reportwkr

import (
	"context"
	"encoding/binary"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/pkg/jetstream"
	"exusiai.dev/backend-next/internal/pkg/observability"
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
// processed under it, and its line in the logs carries the ids of that span. A task without one, as published before
// the publisher set it, starts a trace of its own.
func TestConsumeContinuesThePublishersTrace(t *testing.T) {
	s := newTestStream(t)
	recorder := recordSpans(t)
	var logs lockedBuffer
	prev := log.Logger
	log.Logger = zerolog.New(&logs)
	t.Cleanup(func() { log.Logger = prev })

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
			if kv.Key == observability.AttributeReportTaskID {
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
		attrs := map[string]string{}
		for _, kv := range span.Attributes() {
			attrs[string(kv.Key)] = kv.Value.Emit()
		}
		if attrs["messaging.nats.num_delivered"] != "1" || attrs["report.queue_latency_ms"] == "" || attrs["report.count"] != "0" {
			t.Errorf("consumer span attributes %v, want the deliveries (1), the queue latency and the task's description", attrs)
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
	// the line of each processed task links to its trace if it is sampled: the ids of its consumer span, as Datadog
	// correlates logs and traces
	processed := map[string]map[string]any{}
	for _, entry := range logEntries(t, &logs, "reportwkr.processed") {
		processed[entry["taskId"].(string)] = entry
	}
	for _, taskId := range []string{"traced", "untraced"} {
		span, ok := spans[taskId]
		if !ok {
			continue
		}
		tid, sid := span.SpanContext().TraceID(), span.SpanContext().SpanID()
		entry := processed[taskId]
		if got, want := entry["dd.trace_id"], strconv.FormatUint(binary.BigEndian.Uint64(tid[8:]), 10); got != want {
			t.Errorf("%s logged with dd.trace_id %v, want %s", taskId, got, want)
		}
		if got, want := entry["dd.span_id"], strconv.FormatUint(binary.BigEndian.Uint64(sid[:]), 10); got != want {
			t.Errorf("%s logged with dd.span_id %v, want %s", taskId, got, want)
		}
	}
	if got := processed["traced"]["dd.trace_id"]; got != "5474458728733560230" {
		t.Errorf("traced task logged with dd.trace_id %v, want the publisher's 4bf92f3577b34da6 in decimal", got)
	}
	if entry, ok := processed["not-sampled"]; !ok {
		t.Error("the task not sampled was not logged as processed")
	} else if _, ok := entry["dd.trace_id"]; ok {
		t.Errorf("the task not sampled logged with %v, want no trace id", entry)
	}

	if span, ok := spans["untraced"]; !ok {
		t.Error("no consumer span of the task published without trace context")
	} else if span.Parent().IsValid() || span.SpanContext().TraceID() == traceID {
		t.Errorf("consumer span of the untraced task has parent %v, want a trace of its own", span.Parent())
	}
}
