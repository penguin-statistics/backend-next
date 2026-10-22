package jetstream

import (
	"context"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/propagation"
)

// tracePropagator carries the trace context of a message in its headers: W3C Trace Context (traceparent and
// tracestate), whatever the global propagator is, so that the publisher and the consumer agree on it.
var tracePropagator = propagation.TraceContext{}

// InjectTraceContext sets the trace context of ctx (its span's) in the headers h of a message to publish, so that the
// consumer's span of the message continues that trace. It sets nothing if ctx has no valid span.
func InjectTraceContext(ctx context.Context, h nats.Header) {
	tracePropagator.Inject(ctx, headerCarrier(h))
}

// ExtractTraceContext returns ctx with the trace context carried in the headers h of a message, as InjectTraceContext
// set it, as the remote parent of the spans started from it. Without one (a message published before the publisher
// set it, or by another publisher), ctx is returned as is.
func ExtractTraceContext(ctx context.Context, h nats.Header) context.Context {
	if len(h) == 0 {
		return ctx
	}
	return tracePropagator.Extract(ctx, headerCarrier(h))
}

// headerCarrier is a propagation.TextMapCarrier of the headers of a NATS message. NATS headers are case-sensitive,
// unlike HTTP ones (nats.Header), so the keys are set and read as the propagator names them: lower case, as W3C
// Trace Context spells them.
type headerCarrier nats.Header

func (c headerCarrier) Get(key string) string {
	return nats.Header(c).Get(key)
}

func (c headerCarrier) Set(key, value string) {
	nats.Header(c).Set(key, value)
}

func (c headerCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}
