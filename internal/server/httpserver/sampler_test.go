package httpserver

import (
	"context"
	"testing"

	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestAlwaysSampleNamePrefix(t *testing.T) {
	recorder := func(rate float64) (*tracesdk.TracerProvider, *[]string) {
		names := &[]string{}
		provider := tracesdk.NewTracerProvider(
			tracesdk.WithSampler(tracesdk.ParentBased(alwaysSampleNamePrefix{
				prefix:   "calcwkr.",
				fallback: tracesdk.TraceIDRatioBased(rate),
			})),
			tracesdk.WithSpanProcessor(spanNameRecorder{names}),
		)
		return provider, names
	}

	provider, names := recorder(0)
	tracer := provider.Tracer("test")
	ctx, batch := tracer.Start(context.Background(), "calcwkr.batch")
	_, query := tracer.Start(ctx, "SELECT")
	query.End()
	batch.End()
	for i := 0; i < 100; i++ {
		_, request := tracer.Start(context.Background(), "GET /PenguinStats/api/v2/result/matrix")
		request.End()
	}
	if len(*names) != 2 || (*names)[0] != "SELECT" || (*names)[1] != "calcwkr.batch" {
		t.Errorf("at rate 0, got sampled spans %v, want only the worker batch and its query", *names)
	}

	provider, names = recorder(1)
	_, request := provider.Tracer("test").Start(context.Background(), "GET /PenguinStats/api/v2/result/matrix")
	request.End()
	if len(*names) != 1 {
		t.Errorf("at rate 1, got sampled spans %v, want the request", *names)
	}
}

// spanNameRecorder records the names of sampled spans as they end.
type spanNameRecorder struct{ names *[]string }

func (r spanNameRecorder) OnStart(context.Context, tracesdk.ReadWriteSpan) {}
func (r spanNameRecorder) OnEnd(s tracesdk.ReadOnlySpan) {
	if s.SpanContext().TraceFlags()&trace.FlagsSampled != 0 {
		*r.names = append(*r.names, s.Name())
	}
}
func (r spanNameRecorder) Shutdown(context.Context) error   { return nil }
func (r spanNameRecorder) ForceFlush(context.Context) error { return nil }
