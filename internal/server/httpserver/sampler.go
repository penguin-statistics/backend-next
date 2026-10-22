package httpserver

import (
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"go.opentelemetry.io/otel/trace"
)

// highVolumePaths are the paths of the requests sampled at the configured rate: the report submission, almost all of
// the traffic (98% of the requests), and the probes (Kubernetes, the Datadog agent's scrape), which are periodic and
// alike. The paths are matched without case and without a trailing slash, as the router matches them.
var highVolumePaths = []struct{ method, path string }{
	{fiber.MethodPost, "/PenguinStats/api/v2/report"},
	{fiber.MethodGet, "/api/_/health"},
	{fiber.MethodGet, "/api/_/ping"},
	{fiber.MethodGet, "/metrics"},
}

// lowVolumeRouteSampler samples the root server span of a request to any other path than highVolumePaths, up to
// perSecond per second (with a burst of as many, at least one), and leaves the others, and every span that is not a
// request's, to fallback. A request over the limit is left to fallback too, as every request was before: the limit
// bounds what this sampler adds, and no request is sampled less than it used to be. The reads of the API, the pages and the sitemaps are a few requests per second all together, so that at
// the rate of the report submission (10%) a slow or failed one was rarely traced, and APM's metrics of their routes
// were made of a handful of requests. The limit bounds what a surge of them (a crawler, a purge of the CDN's cache)
// costs.
//
// The sampler sees the span as otelfiber starts it: named after the request's path, with its method among the
// attributes.
type lowVolumeRouteSampler struct {
	fallback tracesdk.Sampler
	limiter  *tokenBucket // nil: none sampled here
}

func newLowVolumeRouteSampler(perSecond float64, fallback tracesdk.Sampler) tracesdk.Sampler {
	if perSecond <= 0 {
		return fallback
	}
	return lowVolumeRouteSampler{fallback: fallback, limiter: newTokenBucket(perSecond, time.Now)}
}

func (s lowVolumeRouteSampler) ShouldSample(p tracesdk.SamplingParameters) tracesdk.SamplingResult {
	if p.Kind == trace.SpanKindServer && !isHighVolume(p) && s.limiter.take() {
		return tracesdk.AlwaysSample().ShouldSample(p)
	}
	return s.fallback.ShouldSample(p)
}

func (s lowVolumeRouteSampler) Description() string {
	return "LowVolumeRouteSampler{" + s.fallback.Description() + "}"
}

func isHighVolume(p tracesdk.SamplingParameters) bool {
	path := strings.TrimSuffix(p.Name, "/")
	var method string
	for _, kv := range p.Attributes {
		if kv.Key == semconv.HTTPMethodKey {
			method = kv.Value.AsString()
			break
		}
	}
	// fiber serves HEAD with the GET handlers (a probe may use either)
	if method == fiber.MethodHead {
		method = fiber.MethodGet
	}
	for _, hv := range highVolumePaths {
		if method == hv.method && strings.EqualFold(path, hv.path) {
			return true
		}
	}
	return false
}

// tokenBucket allows perSecond takes per second on average, and burst at once after a quiet while: perSecond, or one
// for a rate below one per second, which could otherwise never fill a whole token.
type tokenBucket struct {
	mu        sync.Mutex
	perSecond float64
	burst     float64
	tokens    float64
	last      time.Time
	now       func() time.Time
}

func newTokenBucket(perSecond float64, now func() time.Time) *tokenBucket {
	burst := perSecond
	if burst < 1 {
		burst = 1
	}
	return &tokenBucket{perSecond: perSecond, burst: burst, tokens: burst, last: now(), now: now}
}

func (b *tokenBucket) take() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.tokens += now.Sub(b.last).Seconds() * b.perSecond
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
