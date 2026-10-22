package reportwkr

// Integration tests of the spans of processing a task; they need the databases of process_integration_test.go.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"

	"exusiai.dev/backend-next/internal/model/types"
)

// processTraced processes data under a sampled root span, as ingestPreprocess does, and returns the spans ended.
func processTraced(t *testing.T, w *Worker, data []byte) (map[string]tracesdk.ReadOnlySpan, error) {
	t.Helper()
	recorder := recordSpans(t)
	task := &types.ReportTask{}
	if err := json.Unmarshal(data, task); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, span := otel.Tracer("test").Start(ctx, "task")
	err := w.process(ctx, task)
	span.End()
	spans := map[string]tracesdk.ReadOnlySpan{}
	for _, s := range recorder.Ended() {
		spans[s.Name()] = s
	}
	return spans, err
}

func attributeMap(span tracesdk.ReadOnlySpan) map[string]string {
	got := map[string]string{}
	for _, kv := range span.Attributes() {
		got[string(kv.Key)] = kv.Value.Emit()
	}
	return got
}

// The task's span gets the verdict of the verifiers (the reliability of the first report rejected, how many were,
// and why) and the id of the last report stored.
func TestProcessDescribesTheVerdictOnTheTaskSpan(t *testing.T) {
	w := newTestWorker(t)
	rejected := testReport("main_04-04", 30013)
	rejected.Times = 7
	task := newTestTask(t, w, 42, time.UnixMicro(1760600000123456), "203.0.113.7",
		testReport("main_01-07", 30012), testReport("main_01-07", 99999), rejected)

	spans, err := processTraced(t, w, task)
	if err != nil {
		t.Fatal(err)
	}
	got := attributeMap(spans["task"])
	if got["report.reliability"] != "6" || got["report.rejected"] != "2" {
		t.Errorf("report.reliability %q, report.rejected %q, want 6 (the first rejected) and 2", got["report.reliability"], got["report.rejected"])
	}
	if v := got["report.violations"]; strings.Count(v, "; ") != 1 || !strings.Contains(v, "reject rule") {
		t.Errorf("report.violations %q, want the messages of the 2 rejected reports, one of a reject rule", v)
	}
	var last string
	if err := w.DB.NewRaw("SELECT max(report_id)::text FROM drop_reports").Scan(context.Background(), &last); err != nil {
		t.Fatal(err)
	}
	if got["report.id"] != last {
		t.Errorf("report.id %q, want the last report stored, %s", got["report.id"], last)
	}
	for _, name := range []string{"reportwkr.process.Verify", "reportwkr.process.Persistence"} {
		if span, ok := spans[name]; !ok || span.Status().Code != codes.Ok {
			t.Errorf("%s ended with %v, want ok", name, spans[name])
		}
	}
}

// A verification that fails ends the Verify span with the error: Datadog's error.message of the span.
func TestProcessRecordsTheErrorOfAFailedVerification(t *testing.T) {
	w := newTestWorker(t)
	if _, err := w.DB.ExecContext(context.Background(), "ALTER TABLE reject_rules RENAME TO reject_rules_gone"); err != nil {
		t.Fatal(err)
	}
	task := newTestTask(t, w, 42, time.UnixMicro(1760600000123456), "203.0.113.7", testReport("main_01-07", 30012))

	spans, err := processTraced(t, w, task)
	if err == nil {
		t.Fatal("processed without the reject rules")
	}
	verify, ok := spans["reportwkr.process.Verify"]
	if !ok {
		t.Fatal("Verify span not ended")
	}
	if verify.Status().Code != codes.Error || !strings.Contains(verify.Status().Description, "failed to verify report") {
		t.Errorf("Verify span status %v, want the error", verify.Status())
	}
	if events := verify.Events(); len(events) != 1 || events[0].Name != "exception" {
		t.Errorf("Verify span events %v, want the exception", events)
	}
	if _, ok := spans["reportwkr.process.Persistence"]; ok {
		t.Error("persisted a task whose verification failed")
	}
}
