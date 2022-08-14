package httpserver

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/ansrivas/fiberprometheus/v2"
	"github.com/gofiber/contrib/fibersentry"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/favicon"
	"github.com/gofiber/fiber/v2/middleware/pprof"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/helmet/v2"
	"github.com/penguin-statistics/fiberotel"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/pkg/bininfo"
	"github.com/penguin-statistics/backend-next/internal/pkg/middlewares"
	"github.com/penguin-statistics/backend-next/internal/pkg/observability"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
)

var registerPromOnce sync.Once

func Create(conf *config.Config) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "Penguin Stats Backend v3",
		ServerHeader: fmt.Sprintf("Penguin/%s", bininfo.Version),
		// NOTICE: This will also affect WebSocket. Be aware if this fiber instance service is re-used
		//         for long connection services.
		ReadTimeout:    time.Second * 20,
		WriteTimeout:   time.Second * 20,
		ReadBufferSize: 8192,
		// allow possibility for graceful shutdown, otherwise app#Shutdown() will block forever
		IdleTimeout:             conf.HTTPServerShutdownTimeout,
		ProxyHeader:             "X-Original-Forwarded-For",
		EnableTrustedProxyCheck: true,
		TrustedProxies:          conf.TrustedProxies,

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
	registerPromOnce.Do(func() {
		fiberprom := fiberprometheus.New(observability.ServiceName)
		fiberprom.RegisterAt(app, "/metrics")
	})

	if conf.TracingEnabled {
		exporter, err := jaeger.New(jaeger.WithCollectorEndpoint())
		if err != nil {
			panic(err)
		}
		tracerProvider := tracesdk.NewTracerProvider(
			tracesdk.WithSyncer(exporter),
			tracesdk.WithResource(resource.NewWithAttributes(
				semconv.SchemaURL,
				semconv.ServiceNameKey.String("backendv3"),
				attribute.String("environment", "dev"),
			)),
		)
		otel.SetTracerProvider(tracerProvider)

		app.Use(fiberotel.New(fiberotel.Config{
			Tracer:   tracerProvider.Tracer("backendv3"),
			SpanName: "HTTP {{ .Method }} {{ .Path }}",
		}))
	}

	if conf.DevMode {
		log.Info().Msg("Running in DEV mode")
		app.Use(pprof.New())
	}

	if !conf.DevMode {
		app.Use(middlewares.EnrichSentry())
	}

	return app
}
