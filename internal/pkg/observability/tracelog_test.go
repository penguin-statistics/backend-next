package observability

import (
	"bytes"
	"testing"

	"github.com/goccy/go-json"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/trace"
)

func spanContext(t *testing.T, traceID, spanID string, flags trace.TraceFlags) trace.SpanContext {
	t.Helper()
	tid, err := trace.TraceIDFromHex(traceID)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := trace.SpanIDFromHex(spanID)
	if err != nil {
		t.Fatal(err)
	}
	return trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: flags})
}

func TestLogTraceIDs(t *testing.T) {
	for _, tt := range []struct {
		name, traceID, spanID string
		wantTrace, wantSpan   string
	}{
		// the browser RUM SDK's 64-bit trace id, zero-padded in its traceparent: Datadog shows 8448eb211c80319c as
		// 9532127138774266268
		{"rum", "00000000000000008448eb211c80319c", "b7ad6b7169203331", "9532127138774266268", "13235353014750950193"},
		// a 128-bit OpenTelemetry trace id: its lower 64 bits
		{"otel", "d10a02fe7a3349f30b4de86125d9ad07", "0000000000000001", "814562611568422151", "1"},
		{"max", "ffffffffffffffffffffffffffffffff", "ffffffffffffffff", "18446744073709551615", "18446744073709551615"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			traceID, spanID := LogTraceIDs(spanContext(t, tt.traceID, tt.spanID, trace.FlagsSampled))
			if traceID != tt.wantTrace || spanID != tt.wantSpan {
				t.Errorf("LogTraceIDs = %s, %s, want %s, %s", traceID, spanID, tt.wantTrace, tt.wantSpan)
			}
		})
	}
}

// Only a sampled span's ids are logged, as strings (a JSON number would lose the precision of a 64-bit id).
func TestTraceIDsLogged(t *testing.T) {
	sampled := spanContext(t, "00000000000000008448eb211c80319c", "b7ad6b7169203331", trace.FlagsSampled)
	notSampled := spanContext(t, "00000000000000008448eb211c80319c", "b7ad6b7169203331", 0)

	logged := func(write func(l zerolog.Logger)) map[string]any {
		t.Helper()
		var buf bytes.Buffer
		write(zerolog.New(&buf))
		var m map[string]any
		if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	for _, tt := range []struct {
		name  string
		write func(sc trace.SpanContext) func(l zerolog.Logger)
	}{
		{"event", func(sc trace.SpanContext) func(l zerolog.Logger) {
			return func(l zerolog.Logger) { TraceIDs(l.Info(), sc).Msg("m") }
		}},
		{"context", func(sc trace.SpanContext) func(l zerolog.Logger) {
			return func(l zerolog.Logger) {
				l = WithTraceIDs(l.With(), sc).Logger()
				l.Info().Msg("m")
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := logged(tt.write(sampled))
			if m[LogFieldTraceID] != "9532127138774266268" || m[LogFieldSpanID] != "13235353014750950193" {
				t.Errorf("logged %v, want the ids of the sampled span as strings", m)
			}
			m = logged(tt.write(notSampled))
			if _, ok := m[LogFieldTraceID]; ok {
				t.Errorf("logged %v, want no ids of a span not sampled", m)
			}
			if _, ok := m[LogFieldSpanID]; ok {
				t.Errorf("logged %v, want no ids of a span not sampled", m)
			}
		})
	}
}
