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
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/favicon"
	"github.com/gofiber/fiber/v2/middleware/pprof"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/helmet/v2"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"

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
	app.Use(cors.New(cors.Config{
		AllowOrigins:     "*",
		AllowMethods:     "GET, POST, DELETE, OPTIONS",
		AllowHeaders:     "Content-Type, Authorization, X-Requested-With, X-Penguin-Variant, sentry-trace",
		ExposeHeaders:    "Content-Type, X-Penguin-Set-PenguinID, X-Penguin-Upgrade, X-Penguin-Compatible, X-Penguin-Request-ID",
		AllowCredentials: true,
	}))
	// requestid is used by report service to identify requests and generate taskId there afterwards
	// the logger middleware now injects RequestID into the context
	middlewares.Logger(app)
	// then we need an extra middleware to extract it and repopulate it into ctx.Locals
	app.Use(middlewares.RequestID())

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
					semconv.ServiceNameKey.String("pgbackend"),
					semconv.ServiceVersionKey.String(bininfo.Version),
					semconv.ServiceInstanceIDKey.String(lo.Must(os.Hostname())),
					semconv.DeploymentEnvironmentKey.String(lo.Ternary(conf.DevMode, "dev", "prod")),
				)),
				tracesdk.WithSampler(
					tracesdk.ParentBased(
						tracesdk.TraceIDRatioBased(
							conf.TracingSampleRate))),
			},
			tracingProviderOptions(conf)...,
		)...,
	)
	otel.SetTracerProvider(tracerProvider)

	app.Use(otelfiber.Middleware(otelfiber.WithServerName("pgbackend")))

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
