package httpserver

import (
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/bwmarrin/snowflake"
	"github.com/gofiber/contrib/fibersentry"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/favicon"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/pprof"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
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
	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/pkg/bininfo"
	"github.com/penguin-statistics/backend-next/internal/pkg/middlewares"
)

func Create(conf *config.Config, flake *snowflake.Node) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "Penguin Stats Backend v3",
		ServerHeader: fmt.Sprintf("Penguin/%s", bininfo.Version),
		// NOTICE: This will also affect WebSocket. Be aware if this fiber instance service is re-used
		//         for long connection services.
		ReadTimeout:  time.Second * 20,
		WriteTimeout: time.Second * 20,
		// allow possibility for graceful shutdown, otherwise app#Shutdown() will block forever
		IdleTimeout:             conf.HttpServerShutdownTimeout,
		ProxyHeader:             fiber.HeaderXForwardedFor,
		EnableTrustedProxyCheck: true,
		TrustedProxies: []string{
			"::1",
			"127.0.0.1",
		},
		ErrorHandler: ErrorHandler,
	})

	app.Use(favicon.New())
	app.Use(fibersentry.New(fibersentry.Config{
		Repanic: true,
		Timeout: time.Second * 5,
	}))
	app.Use(cors.New(cors.Config{
		AllowOrigins:     "*",
		AllowMethods:     "GET, POST, DELETE, OPTIONS",
		AllowHeaders:     "Content-Type, Authorization, X-Requested-With, X-Penguin-Variant",
		ExposeHeaders:    "Content-Type, X-Penguin-Set-PenguinID, X-Penguin-Upgrade, X-Penguin-Compatible, X-Penguin-Request-ID",
		AllowCredentials: true,
	}))
	app.Use(requestid.New(
		requestid.Config{
			Header: "X-Penguin-Request-ID",
			Generator: func() string {
				id := flake.Generate().IntBytes()
				return hex.EncodeToString(id[:])
			},
			ContextKey: constants.ContextKeyRequestID,
		},
	))
	app.Use(helmet.New(helmet.Config{
		HSTSMaxAge:         31356000,
		HSTSPreloadEnabled: true,
		ReferrerPolicy:     "strict-origin-when-cross-origin",
		PermissionPolicy:   "interest-cohort=()",
	}))
	app.Use(middlewares.EnrichSentry())
	app.Use(middlewares.InjectI18n())
	app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
		StackTraceHandler: func(c *fiber.Ctx, e interface{}) {
			buf := make([]byte, 4096)
			buf = buf[:runtime.Stack(buf, false)]
			log.Error().Msgf("panic: %v\n%s\n", e, buf)
		},
	}))
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

		app.Use(logger.New(logger.Config{
			Format:     "${pid} ${locals:requestid} ${status} ${latency}\t${ip}\t- ${method} ${url}\n",
			TimeFormat: time.RFC3339,
			Output:     os.Stdout,
		}))
	} else {
		// app.Use(limiter.New(limiter.Config{
		// 	Max:        30,
		// 	Expiration: time.Minute,
		// }))
		app.Use(logger.New(logger.Config{
			Format:     "${pid} ${locals:requestid} ${status} ${latency}\t${ip}\t- ${method} ${url}\n",
			TimeFormat: time.RFC3339,
			Output:     log.Logger,
		}))
	}

	return app
}
