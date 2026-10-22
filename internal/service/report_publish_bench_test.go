package service

import (
	"sync"
	"testing"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	"github.com/nats-io/nats.go"
	"github.com/valyala/fasthttp"
	"go.opentelemetry.io/otel"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"exusiai.dev/backend-next/internal/model/types"
)

// ackingPublisher acknowledges every publish at once, as a stream storing it.
type ackingPublisher struct {
	nats.JetStreamContext
}

func (ackingPublisher) Publish(string, []byte, ...nats.PubOpt) (*nats.PubAck, error) {
	return &nats.PubAck{}, nil
}

func (ackingPublisher) PublishMsg(*nats.Msg, ...nats.PubOpt) (*nats.PubAck, error) {
	return &nats.PubAck{}, nil
}

var benchTracerProvider sync.Once

// BenchmarkCommitReportTask publishes a report task in a request whose server span is sampled or not (the request
// sampling of production), with an idempotency key, to a stream that acknowledges at once: the cost of publishing
// besides the round trip to NATS.
func BenchmarkCommitReportTask(b *testing.B) {
	benchTracerProvider.Do(func() {
		// production's sampling of a span with a parent; the spans are recorded, not exported
		otel.SetTracerProvider(tracesdk.NewTracerProvider(tracesdk.WithSampler(tracesdk.ParentBased(tracesdk.NeverSample()))))
	})
	s := &Report{NatsJS: ackingPublisher{}}
	task := &types.ReportTask{
		FragmentReportCommon: types.FragmentReportCommon{Server: "CN", Source: "MaaAssistantArknights", Version: "v6.20.1"},
		Reports:              []*types.ReportTaskSingleReport{{FragmentStageID: types.FragmentStageID{StageID: "main_01-07"}, Drops: []*types.Drop{{DropType: "REGULAR", ItemID: 1, Quantity: 2}}, Times: 1}},
		AccountID:            1,
		IP:                   "203.0.113.7",
	}

	for _, bc := range []struct {
		name  string
		flags trace.TraceFlags
	}{
		{"not sampled", 0},
		{"sampled", trace.FlagsSampled},
	} {
		b.Run(bc.name, func(b *testing.B) {
			request := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: bc.flags})
			app := fiber.New()
			app.Post("/", func(c *fiber.Ctx) error {
				c.Locals(constant.ContextKeyRequestID, "req")
				c.Locals(constant.IdempotencyKeyLocalsKey, "key")
				c.SetUserContext(trace.ContextWithSpanContext(c.UserContext(), request))
				if _, err := s.commitReportTask(c, "REPORT.SINGLE", task); err != nil {
					b.Fatal(err)
				}
				return nil
			})
			handler := app.Handler()
			var req fasthttp.Request
			req.Header.SetMethod(fiber.MethodPost)
			req.SetRequestURI("/")
			var ctx fasthttp.RequestCtx
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ctx.ResetUserValues()
				ctx.Init(&req, nil, nil)
				handler(&ctx)
			}
		})
	}
}
