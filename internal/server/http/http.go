package httpserver

import (
	"encoding/hex"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/bwmarrin/snowflake"
	"github.com/davecgh/go-spew/spew"
	ut "github.com/go-playground/universal-translator"
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
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"golang.org/x/text/language"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
	"github.com/penguin-statistics/backend-next/internal/utils/i18n"
)

func CreateServer(config *config.Config, flake *snowflake.Node) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "Penguin Stats Backend v3",
		ServerHeader: "Penguin/0.1",
		// NOTICE: This will also affect WebSocket. Be aware if this fiber instance service is re-used
		//         for long connection services.
		ReadTimeout:  time.Second * 20,
		WriteTimeout: time.Second * 20,
		// allow possibility for graceful shutdown, otherwise app#Shutdown() will block forever
		IdleTimeout: config.HttpServerShutdownTimeout,
		ProxyHeader: fiber.HeaderXForwardedFor,
		ErrorHandler: func(ctx *fiber.Ctx, err error) error {
			// Use custom error handler to return JSON error responses
			if e, ok := err.(*errors.PenguinError); ok {
				log.Warn().
					Err(err).
					Str("method", ctx.Method()).
					Str("path", ctx.Path()).
					Msg(e.Message)

				// Provide error code if errors.PenguinError type
				body := fiber.Map{
					"code":    e.ErrorCode,
					"message": e.Message,
				}

				// Add extra details if needed
				if e.Extras != nil && len(*e.Extras) > 0 {
					for k, v := range *e.Extras {
						body[k] = v
					}
				}

				return ctx.Status(e.StatusCode).JSON(body)
			}

			// Return default error handler
			// Default 500 statuscode
			code := fiber.StatusInternalServerError

			if e, ok := err.(*fiber.Error); ok {
				// Overwrite status code if fiber.Error type & provided code
				code = e.Code
			}

			s := pkgerrors.MarshalStack(err)
			spew.Dump(s, err)

			log.Error().
				Stack().
				Err(err).
				Str("method", ctx.Method()).
				Str("path", ctx.Path()).
				Int("status", code).
				Msg("Internal Server Error")

			return ctx.Status(code).JSON(fiber.Map{
				"code":    errors.CodeInternalError,
				"message": err.Error(),
			})
		},
	})

	app.Use(favicon.New())
	app.Use(cors.New(cors.Config{
		AllowMethods:     "GET, POST, DELETE, OPTIONS",
		AllowHeaders:     "Content-Type, Authorization, X-Requested-With, X-Penguin-Variant",
		ExposeHeaders:    "Content-Type, X-Penguin-Set-PenguinID, X-Penguin-Upgrade, X-Penguin-Compatible, X-Penguin-RequestID",
		AllowCredentials: true,
	}))
	app.Use(requestid.New(
		requestid.Config{
			Header: "X-Penguin-RequestID",
			Generator: func() string {
				id := flake.Generate().IntBytes()
				return hex.EncodeToString(id[:])
			},
		},
	))
	app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
		StackTraceHandler: func(c *fiber.Ctx, e interface{}) {
			os.Stderr.WriteString(fmt.Sprintf("panic: %v\n%s\n", e, string(debug.Stack())))
		},
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
	app.Use(func(c *fiber.Ctx) error {
		set := func(trans ut.Translator) error {
			c.Locals("T", trans)
			return c.Next()
		}

		tags, _, err := language.ParseAcceptLanguage(c.Get("Accept-Language"))
		if err != nil {
			return set(i18n.UT.GetFallback())
		}

		var langs []string

		for _, tag := range tags {
			langs = append(langs, strings.ReplaceAll(strings.ToLower(tag.String()), "-", "_"))
		}

		trans, _ := i18n.UT.FindTranslator(langs...)

		return set(trans)
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
