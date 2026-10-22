package service

import (
	"sync"
	"testing"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/pkg/observability"
)

var installTracerProvider sync.Once

// recordSpans returns a recorder of the spans ended during the test, by a tracer provider sampling as production's
// does for a request carrying a trace context: as its parent.
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

// A task is published with the trace context of a publish span, a child of the request's span, in its headers
// (traceparent and tracestate), for the report worker to continue the request's trace; the idempotency key is still
// its Nats-Msg-Id. The publish span is sampled as the request is.
func TestCommitReportTaskCarriesTraceContext(t *testing.T) {
	s, _, prefix := newTestPublisher(t)
	stream := addTestStream(t, s, prefix)

	traceID, _ := trace.TraceIDFromHex("00000000000000004bf92f3577b34da6")
	requestSpanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	state, _ := trace.ParseTraceState("dd=s:1;o:rum")

	for _, tt := range []struct {
		name  string
		flags trace.TraceFlags
	}{
		{"sampled", trace.FlagsSampled},
		{"not sampled", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := recordSpans(t)
			// the request's server span, as otelfiber sets it in the user context
			request := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: requestSpanID, TraceFlags: tt.flags, TraceState: state})
			key := "key-" + tt.name
			taskId, err := commitInRequest(t, s, prefix+".SINGLE", func(c *fiber.Ctx) {
				c.SetUserContext(trace.ContextWithSpanContext(c.UserContext(), request))
				c.Locals(constant.IdempotencyKeyLocalsKey, key)
			})
			if err != nil {
				t.Fatal(err)
			}

			msg, err := s.NatsJS.GetLastMsg(stream, prefix+".SINGLE")
			if err != nil {
				t.Fatal(err)
			}
			if got := msg.Header.Get(nats.MsgIdHdr); got != key {
				t.Errorf("%s = %q, want the idempotency key %q", nats.MsgIdHdr, got, key)
			}
			if got := msg.Header.Get("tracestate"); got != "dd=s:1;o:rum" {
				t.Errorf("tracestate = %q, want the request's", got)
			}
			traceparent := msg.Header.Get("traceparent")

			spans := recorder.Ended()
			if tt.flags.IsSampled() {
				if len(spans) != 1 || spans[0].Name() != "report.PublishTask" {
					t.Fatalf("recorded %v, want the publish span", spans)
				}
				span := spans[0]
				if span.SpanKind() != trace.SpanKindProducer || span.Parent().SpanID() != requestSpanID || span.SpanContext().TraceID() != traceID {
					t.Errorf("publish span %v of parent %v, want a producer span child of the request's span", span.SpanContext(), span.Parent())
				}
				if want := "00-" + traceID.String() + "-" + span.SpanContext().SpanID().String() + "-01"; traceparent != want {
					t.Errorf("traceparent = %q, want the publish span's %q", traceparent, want)
				}
				var taskIdAttr string
				for _, kv := range span.Attributes() {
					if kv.Key == observability.AttributeReportTaskID {
						taskIdAttr = kv.Value.AsString()
					}
				}
				if taskIdAttr != taskId {
					t.Errorf("report.task_id = %q, want %q", taskIdAttr, taskId)
				}
			} else {
				if len(spans) != 0 {
					t.Errorf("recorded %d spans, want none: the request is not sampled", len(spans))
				}
				// the trace and the decision not to sample it carry on
				if len(traceparent) != 55 || traceparent[3:35] != traceID.String() || traceparent[53:] != "00" {
					t.Errorf("traceparent = %q, want the request's trace, not sampled", traceparent)
				}
			}
		})
	}
}

// The request's server span, the root of the trace, gets what the request reported, when it is sampled: Datadog
// searches and groups the traces of reports by it.
func TestCommitReportTaskDescribesTheReportOnTheRequestSpan(t *testing.T) {
	s, _, prefix := newTestPublisher(t)
	addTestStream(t, s, prefix)
	recorder := recordSpans(t)

	task := &types.ReportTask{
		FragmentReportCommon: types.FragmentReportCommon{Server: "CN", Source: "MeoAssistant", Version: "v5.0.0"},
		Reports: []*types.ReportTaskSingleReport{{
			FragmentStageID: types.FragmentStageID{StageID: "main_01-07"},
			Drops:           []*types.Drop{{ItemID: 1, Quantity: 2}, {ItemID: 2, Quantity: 1}},
			Times:           3,
		}},
		AccountID: 42,
	}
	var requestSpan trace.Span
	taskId, err := commitTaskInRequest(t, s, prefix+".SINGLE", task, func(c *fiber.Ctx) {
		var ctx = c.UserContext()
		ctx, requestSpan = otel.Tracer("test").Start(ctx, "request", trace.WithSpanKind(trace.SpanKindServer))
		c.SetUserContext(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	requestSpan.End()

	var request tracesdk.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == "request" {
			request = span
		}
	}
	if request == nil {
		t.Fatal("request span not recorded")
	}
	got := map[string]string{}
	for _, kv := range request.Attributes() {
		got[string(kv.Key)] = kv.Value.Emit()
	}
	for k, want := range map[string]string{
		"report.task_id":         taskId,
		"report.server":          "CN",
		"report.source":          "MeoAssistant",
		"report.source_version":  "v5.0.0",
		"report.source_category": "maa",
		"report.count":           "1",
		"report.stage_id":        "main_01-07",
		"report.drops":           "2",
		"report.times":           "3",
		"usr.account_id":         "42",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}
