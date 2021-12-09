package httpserver

import (
	"fmt"
	"os"
	"time"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/favicon"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/pprof"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/gofiber/helmet/v2"
	"github.com/gofiber/websocket/v2"

	"github.com/penguin-statistics/fiberotel"
)

func CreateServer(config *config.Config) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "Penguin Stats Backend v3",
		ServerHeader: "Penguin/0.1",
		// NOTICE: This will also affect WebSocket. Be aware if this fiber instance service is re-used
		//         for long connection services.
		ReadTimeout:  time.Second * 20,
		WriteTimeout: time.Second * 20,
		// allow possibility for graceful shutdown, otherwise app#Shutdown() will block forever
		IdleTimeout: time.Second * 600,
		ProxyHeader: fiber.HeaderXForwardedFor,
		ErrorHandler: func(ctx *fiber.Ctx, err error) error {
			// Use custom error handler to return JSON error responses
			if e, ok := err.(*errors.PenguinError); ok {
				// Provide error code if errors.PenguinError type
				return ctx.Status(e.StatusCode).JSON(fiber.Map{
					"code":    e.ErrorCode,
					"message": e.Message,
				})
			}

			// Return default error handler
			// Default 500 statuscode
			code := fiber.StatusInternalServerError

			if e, ok := err.(*fiber.Error); ok {
				// Overwrite status code if fiber.Error type & provided code
				code = e.Code
			}

			return ctx.Status(code).JSON(fiber.Map{
				"code":    "INTERNAL_ERROR",
				"message": err.Error(),
			})
		},
	})

	app.Use(favicon.New())
	app.Use(requestid.New(
		requestid.Config{
			Generator: func(ctx *fiber.Ctx) string {
				return ctx.Get(requestid.Header)
			},
		},
	))
	app.Use(cors.New())
	app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
	}))
	app.Use(helmet.New(helmet.Config{
		HSTSMaxAge:         31356000,
		HSTSPreloadEnabled: true,
		ReferrerPolicy:     "strict-origin-when-cross-origin",
		// ContentSecurityPolicy: "default-src 'none'; script-src 'none'; worker-src 'none'; frame-ancestors 'none'; sandbox",
		PermissionPolicy: "interest-cohort=()",
	}))
	app.Use(compress.New(compress.Config{
		Level: compress.LevelBestSpeed,
	}))
	app.Use("/api/v3/live", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})

	if config.DevMode {
		fmt.Println("Running in DEV mode")

		app.Use(pprof.New())

		app.Use(logger.New(logger.Config{
			Format:     "${pid} ${ip} ${locals:requestid} ${status} ${latency} - ${method} ${path}\n",
			TimeFormat: time.RFC3339,
			Output:     os.Stdout,
		}))

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

		// fjaeger.New(fjaeger.Config{
		// 	ServiceName: "backendv3",
		// })

		// app.Use(fibertracing.New(fibertracing.Config{
		// 	Tracer: opentracing.GlobalTracer(),
		// 	OperationName: func(ctx *fiber.Ctx) string {
		// 		return ctx.Method() + " " + ctx.Path()
		// 	},
		// 	Modify: func(ctx *fiber.Ctx, span opentracing.Span) {
		// 		span.SetTag("http.method", ctx.Method()) // GET, POST
		// 		span.SetTag("http.remote_address", ctx.IP())
		// 		span.SetTag("http.path", ctx.Path())
		// 		span.SetTag("http.host", ctx.Hostname())
		// 		span.SetTag("http.url", ctx.OriginalURL())

		// 		ctx.SetUserContext(opentracing.ContextWithSpan(ctx.Context(), span))
		// 	},
		// }))
	}

	return app
}
