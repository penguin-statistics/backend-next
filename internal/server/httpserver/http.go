package httpserver

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ansrivas/fiberprometheus/v2"
	"github.com/goccy/go-json"
	"github.com/gofiber/contrib/fibersentry"
	"github.com/gofiber/contrib/otelfiber"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/favicon"
	"github.com/gofiber/fiber/v2/middleware/pprof"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/helmet/v2"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/pkg/bininfo"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
)

var prometheusRegisterOnce sync.Once

type DevOpsApp struct {
	*fiber.App
}

func Create(conf *appconfig.Config) (*fiber.App, DevOpsApp) {
	return CreateServiceApp(conf), DevOpsApp{
		App: CreateDevOpsApp(conf),
	}
}

func CreateServiceApp(conf *appconfig.Config) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:               "Penguin Stats Backend v3",
		ServerHeader:          fmt.Sprintf("Penguin/%s", bininfo.Version),
		DisableStartupMessage: !conf.DevMode,
		// NOTICE: This will also affect WebSocket. Be aware if this fiber instance service is re-used
		//         for long connection services.
		ReadTimeout:  time.Second * 20,
		WriteTimeout: time.Second * 20,
		// allow possibility for graceful shutdown, otherwise app#Shutdown() will block forever
		IdleTimeout:             conf.HTTPServerShutdownTimeout,
		ProxyHeader:             "X-Original-Forwarded-For",
		EnableTrustedProxyCheck: true,
		TrustedProxies:          conf.TrustedProxies,

		JSONEncoder: json.Marshal,
		JSONDecoder: json.Unmarshal,

		ErrorHandler: ErrorHandler,
		Immutable:    true,
	})

	app.Use(favicon.New())
	app.Use(fibersentry.New(fibersentry.Config{
		Repanic: true,
		Timeout: time.Second * 5,
	}))
	app.Use(middlewares.CORS(conf.CORSCredentialedOrigins))
	// a 429 without its own Retry-After (fiber's limiter sets one) tells clients to wait a minute
	app.Use(middlewares.RetryAfter(time.Minute))
	// the API links its responses to its spec, docs, data license and catalog, and keeps them out of search indexes;
	// not the stats pages, the sitemaps, /swagger or /mcp. Registered with the app, before any route, so that it also
	// covers the routes on the app itself (GET /api) and the 404 of an unknown path under these prefixes.
	app.Use([]string{"/PenguinStats", "/api"}, middlewares.APIDiscovery())
	// requestid is used by report service to identify requests and generate taskId there afterwards
	// the logger middleware now injects RequestID into the context
	middlewares.Logger(app)
	// then we need an extra middleware to extract it and repopulate it into ctx.Locals
	app.Use(middlewares.RequestID())

	// Compress responses, as neither ingress-nginx nor cloudflared does. It wraps the handlers and the /result
	// response cache, so the cache keeps uncompressed bodies and every response is compressed again. LevelDefault is
	// brotli 4 / gzip 6: BestSpeed's brotli 0 is no smaller than gzip 1 (the boot responses are 888 KB at brotli 0 vs
	// 630 KB at brotli 4, for about 20 ms more per large response), and BestCompression's brotli 11 takes seconds.
	// See compressbench_test.go.
	app.Use(compress.New(compress.Config{
		Level: compress.LevelDefault,
	}))
	// fasthttp adds Vary: Accept-Encoding only to the responses it compresses, but the identity response to a request
	// without Accept-Encoding varies on it too: without the header a shared cache could serve that uncompressed body
	// to clients that accept compression. Errors are left to ErrorHandler and are not compressed, so they do not vary.
	app.Use(func(c *fiber.Ctx) error {
		if err := c.Next(); err != nil {
			return err
		}
		c.Vary(fiber.HeaderAcceptEncoding)
		return nil
	})

	app.Use(func(c *fiber.Ctx) error {
		// Use custom error handler to return customized error responses
		err := c.Next()
		if e, ok := err.(*pgerr.PenguinError); ok {
			return HandleCustomError(c, e)
		}
		return err
	})

	app.Use(helmet.New(helmet.Config{
		HSTSMaxAge:         31356000,
		HSTSPreloadEnabled: true,
		ReferrerPolicy:     "strict-origin-when-cross-origin",
		PermissionPolicy:   "interest-cohort=()",
	}))
	app.Use(middlewares.InjectI18n())
	app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
		StackTraceHandler: func(c *fiber.Ctx, e any) {
			buf := make([]byte, 4096)
			buf = buf[:runtime.Stack(buf, false)]
			log.Error().Msgf("panic: %v\n%s\n", e, buf)
		},
	}))

	tracerProvider := tracesdk.NewTracerProvider(
		append(
			[]tracesdk.TracerProviderOption{
				tracesdk.WithResource(resource.NewWithAttributes(
					semconv.SchemaURL,
					append([]attribute.KeyValue{
						semconv.ServiceNameKey.String("pgbackend"),
						semconv.ServiceVersionKey.String(bininfo.Version),
						semconv.ServiceInstanceIDKey.String(lo.Must(os.Hostname())),
						semconv.DeploymentEnvironmentKey.String(lo.Ternary(conf.DevMode, "dev", "prod")),
					}, gitAttributes()...)...,
				)),
				tracesdk.WithSampler(
					tracesdk.ParentBased(
						alwaysSampleNamePrefix{
							prefix: "calcwkr.",
							fallback: newLowVolumeRouteSampler(
								conf.TracingLowVolumeTracesPerSecond,
								tracesdk.TraceIDRatioBased(conf.TracingSampleRate)),
						})),
			},
			tracingProviderOptions(conf)...,
		)...,
	)
	otel.SetTracerProvider(tracerProvider)
	// W3C Trace Context, as the browser RUM SDK sends with tracecontext propagation: the server span of a request that
	// carries a traceparent continues the caller's trace, and the ParentBased sampler follows the caller's sampled flag,
	// so that a RUM resource finds its backend trace. Without a propagator (OTel's default is a no-op) every request
	// starts a trace of its own. Baggage is not read: nothing here uses it, and it would be parsed from every request
	// that sends it. otelfiber reads the global propagator once, as it is built: set it before.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	app.Use(otelfiber.Middleware(
		otelfiber.WithServerName("pgbackend"),
		otelfiber.WithPropagators(otel.GetTextMapPropagator()),
	))
	// the ids of the server span in the request log (Logger, which logs after the span ended)
	app.Use(middlewares.LogTraceIDs())
	// who made the request and what came of it, on the server span; right after otelfiber, see EnrichServerSpan
	app.Use(middlewares.EnrichServerSpan())

	prometheusRegisterOnce.Do(func() {
		fiberprometheus.New(observability.ServiceName).RegisterAt(app, "/metrics")
	})

	if conf.DevMode {
		log.Info().
			Str("evt.name", "infra.dev_mode.enabled").
			Msg("running in DEV mode")
	}

	if !conf.DevMode {
		app.Use(middlewares.EnrichSentry())
	}

	return app
}

func CreateDevOpsApp(conf *appconfig.Config) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:               "Penguin Stats Backend v3 (DevOps)",
		ServerHeader:          fmt.Sprintf("PenguinDevOps/%s", bininfo.Version),
		DisableStartupMessage: !conf.DevMode,
		// allow possibility for graceful shutdown, otherwise app#Shutdown() will block forever
		IdleTimeout:             conf.HTTPServerShutdownTimeout,
		ProxyHeader:             "X-Original-Forwarded-For",
		EnableTrustedProxyCheck: true,
		TrustedProxies:          conf.TrustedProxies,

		ErrorHandler: ErrorHandler,
		Immutable:    true,
	})

	app.Use(pprof.New())

	app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
		StackTraceHandler: func(c *fiber.Ctx, e any) {
			buf := make([]byte, 4096)
			buf = buf[:runtime.Stack(buf, false)]
			log.Error().Msgf("panic: %v\n%s\n", e, buf)
		},
	}))

	return app
}

// gitAttributes returns the commit of the binary and its repository as Datadog's source code integration reads them on
// spans (the agent turns the resource attributes into span tags), to link a span to its code; none if the commit is
// unknown.
func gitAttributes() []attribute.KeyValue {
	commit := bininfo.Commit()
	if commit == "" {
		return nil
	}
	return []attribute.KeyValue{
		attribute.String("git.commit.sha", commit),
		attribute.String("git.repository_url", bininfo.RepositoryURL),
	}
}

func tracingProviderOptions(conf *appconfig.Config) []tracesdk.TracerProviderOption {
	options := []tracesdk.TracerProviderOption{}
	if !conf.TracingEnabled {
		log.Info().
			Str("evt.name", "infra.tracing.disabled").
			Msg("Tracing is disabled: no spans will be reported")
		return options
	}

	optionsstr := make([]string, 0)

	if conf.TracingExporters != nil {
		exporters := lo.Uniq(conf.TracingExporters)
		for _, exporter := range exporters {
			switch exporter {
			case "jaeger":
				exp := lo.Must(jaeger.New(jaeger.WithAgentEndpoint()))
				options = append(options, tracesdk.WithBatcher(exp))
				optionsstr = append(optionsstr, "jaeger")
			case "otlpgrpc":
				exp := lo.Must(otlptrace.New(context.Background(), otlptracegrpc.NewClient()))
				options = append(options, tracesdk.WithBatcher(exp))
				optionsstr = append(optionsstr, "otlpgrpc")
			case "stdout":
				exp := lo.Must(stdouttrace.New())
				options = append(options, tracesdk.WithSyncer(exp))
				optionsstr = append(optionsstr, "stdout")
			}
		}
	}

	if len(options) == 0 {
		log.Warn().
			Str("evt.name", "infra.tracing.exporters").
			Msg("Tracing is enabled via configuration, but no tracing exporters are provided")
	} else {
		log.Info().
			Str("evt.name", "infra.tracing.exporters").
			Msgf("Tracing enabled with exporters: %s", strings.Join(optionsstr, ", "))
	}

	return options
}

// alwaysSampleNamePrefix samples every root span whose name has prefix, and leaves the others to fallback. Worker
// batches are rare (every 30 minutes) but long, so a request-sized sample rate would miss most of them.
type alwaysSampleNamePrefix struct {
	prefix   string
	fallback tracesdk.Sampler
}

func (s alwaysSampleNamePrefix) ShouldSample(p tracesdk.SamplingParameters) tracesdk.SamplingResult {
	if strings.HasPrefix(p.Name, s.prefix) {
		return tracesdk.AlwaysSample().ShouldSample(p)
	}
	return s.fallback.ShouldSample(p)
}

func (s alwaysSampleNamePrefix) Description() string {
	return "AlwaysSampleNamePrefix{" + s.prefix + "," + s.fallback.Description() + "}"
}
