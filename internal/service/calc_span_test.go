package service

import (
	"context"
	"testing"

	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// The span of a matrix job tells what it calculated; that of a first run of the day, the day before too.
func TestDescribeCalcJob(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tracer := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(recorder)).Tracer("test")

	ctx, span := tracer.Start(context.Background(), "calcwkr.dropMatrix")
	describeCalcJob(ctx, 20383, []string{"all", "automated"}, 1234)
	describePreviousDay(ctx, 1200)
	span.End()
	// a span not recording is left alone
	describeCalcJob(context.Background(), 1, nil, 1)

	got := map[string]string{}
	for _, kv := range recorder.Ended()[0].Attributes() {
		got[string(kv.Key)] = kv.Value.Emit()
	}
	for k, want := range map[string]string{
		"calc.day_num": "20383", "calc.source_categories": "all,automated", "calc.elements": "1234",
		"calc.previous_day": "true", "calc.previous_day_elements": "1200",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}
