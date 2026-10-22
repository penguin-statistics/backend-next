package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/pkg/observability"
)

// fakePublisher answers PublishMsg with ack or err, as a publish that the stream stored, or that failed with err.
type fakePublisher struct {
	nats.JetStreamContext
	ack *nats.PubAck
	err error
}

func (p *fakePublisher) PublishMsg(*nats.Msg, ...nats.PubOpt) (*nats.PubAck, error) {
	return p.ack, p.err
}

func TestCommitReportTaskPublishMetrics(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name      string
		result    string
		publisher *fakePublisher
		ctx       context.Context
	}{
		{"ok", "ok", &fakePublisher{ack: &nats.PubAck{}}, context.Background()},
		{"error", "error", &fakePublisher{err: errors.New("nats: test")}, context.Background()},
		// the publish ends with the context of the request, or at natsPublishTimeout
		{"ctx_cancelled", "ctx_cancelled", &fakePublisher{err: context.Canceled}, cancelled},
		{"timeout", "timeout", &fakePublisher{err: context.DeadlineExceeded}, context.Background()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			counter := observability.ReportPublishTotal.WithLabelValues(tc.result)
			before := testutil.ToFloat64(counter)
			durationsBefore := publishDurations(t).GetSampleCount()

			s := &Report{NatsJS: tc.publisher}
			var err error
			app := fiber.New()
			app.Get("/", func(ctx *fiber.Ctx) error {
				ctx.Locals(constant.ContextKeyRequestID, "test")
				ctx.SetUserContext(tc.ctx)
				_, err = s.commitReportTask(ctx, "REPORT.SINGLE", &types.ReportTask{})
				return nil
			})
			if _, testErr := app.Test(httptest.NewRequest("GET", "/", nil)); testErr != nil {
				t.Fatal(testErr)
			}
			if (err == nil) != (tc.result == "ok") {
				t.Fatalf("unexpected error %v", err)
			}
			if tc.result == "timeout" && err != ErrNatsTimeout {
				t.Fatalf("got error %v, want ErrNatsTimeout", err)
			}

			if got := testutil.ToFloat64(counter) - before; got != 1 {
				t.Errorf("publish_total{%s} increased by %v, want 1", tc.result, got)
			}
			if got := publishDurations(t).GetSampleCount() - durationsBefore; got != 1 {
				t.Errorf("publish_duration_seconds observed %d samples, want 1", got)
			}
		})
	}
}

func publishDurations(t *testing.T) *dto.Histogram {
	t.Helper()
	m := &dto.Metric{}
	if err := observability.ReportPublishDuration.Write(m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram()
}
