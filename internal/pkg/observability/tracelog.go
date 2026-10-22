package observability

import (
	"encoding/binary"
	"strconv"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/trace"
)

// The log attributes of Datadog's log-trace correlation (its JSON preprocessing reads them as the trace and span ids
// of a log line): a log line carrying them links to its trace in APM, and the trace to its logs.
const (
	LogFieldTraceID = "dd.trace_id"
	LogFieldSpanID  = "dd.span_id"
)

// LogTraceIDs returns the trace and span ids of sc as Datadog correlates logs and traces: the lower 64 bits of the
// trace id (a trace ingested over OTLP has the lower 64 bits of the OpenTelemetry trace id as its Datadog trace id, and
// a trace started by the browser RUM SDK has 64-bit ids, zero-padded in its traceparent) and the span id, in decimal.
func LogTraceIDs(sc trace.SpanContext) (traceID, spanID string) {
	tid, sid := sc.TraceID(), sc.SpanID()
	return strconv.FormatUint(binary.BigEndian.Uint64(tid[8:]), 10), strconv.FormatUint(binary.BigEndian.Uint64(sid[:]), 10)
}

// WithTraceIDs adds the trace and span ids of sc to a logger's context, if sc is sampled: the spans of a trace not
// sampled are not exported, so their ids would link to no trace.
func WithTraceIDs(c zerolog.Context, sc trace.SpanContext) zerolog.Context {
	if !sc.IsSampled() {
		return c
	}
	traceID, spanID := LogTraceIDs(sc)
	return c.Str(LogFieldTraceID, traceID).Str(LogFieldSpanID, spanID)
}

// TraceIDs adds the trace and span ids of sc to a log event, if sc is sampled (see WithTraceIDs).
func TraceIDs(e *zerolog.Event, sc trace.SpanContext) *zerolog.Event {
	if !sc.IsSampled() || !e.Enabled() {
		return e
	}
	traceID, spanID := LogTraceIDs(sc)
	return e.Str(LogFieldTraceID, traceID).Str(LogFieldSpanID, spanID)
}
