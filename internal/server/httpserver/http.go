package httpserver

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/ansrivas/fiberprometheus/v2"
	"github.com/gofiber/contrib/fibersentry"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cache"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/favicon"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/pprof"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/utils"
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
		ProxyHeader:             fiber.HeaderXForwardedFor,
		EnableTrustedProxyCheck: true,
		TrustedProxies: []string{
			"::1",
			"127.0.0.1",
			"10.0.0.0/8",
		},
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
	// app.Use(func(c *fiber.Ctx) error {
	// 	i := xid.New()
	// 	c.Locals(constant.ContextKeyRequestID, i.String())
	// 	c.Set("X-Penguin-Request-ID", i.String())
	// 	return c.Next()
	// })
	middlewares.Logger(app)
	// the logger middleware injects RequestID into the context,
	// and we need an extra middleware to extract it and repopulate it into ctx.Locals
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
		// app.Use(limiter.New(limiter.Config{
		// 	Max:        30,
		// 	Expiration: time.Minute,
		// }))

		// Cache requests with itemFilter and stageFilter as there appears to be an unknown source requesting
		// with such behaviors very eagerly, causing a relatively high load on the database.
		log.Info().Msg("enabling fiber-level cache & limiter for all requests containing itemFilter or stageFilter query params.")
		app.Use(limiter.New(limiter.Config{
			Next: func(c *fiber.Ctx) bool {
				if c.Query("itemFilter") != "" || c.Query("stageFilter") != "" {
					return false
				}
				return true
			},
			LimitReached: func(c *fiber.Ctx) error {
				return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
					"code":    "TOO_MANY_REQUESTS",
					"message": "Your client is sending requests too frequently. The Penguin Stats result matrix are updated periodically and should not be requested too frequently.",
				})
			},
			Max:        300,
			Expiration: time.Minute * 5,
		}))

		app.Use(cache.New(cache.Config{
			Next: func(c *fiber.Ctx) bool {
				// only cache requests with itemFilter and stageFilter query params
				if c.Query("itemFilter") != "" || c.Query("stageFilter") != "" {
					time.Sleep(time.Second) // simulate a slow request
					return false
				}
				return true
			},
			CacheHeader:  "X-Penguin-Cache",
			CacheControl: true,
			Expiration:   time.Minute * 5,
			KeyGenerator: func(c *fiber.Ctx) string {
				return utils.CopyString(c.OriginalURL())
			},
		}))
	}

	return app
}
