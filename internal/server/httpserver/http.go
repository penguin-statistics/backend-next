package httpserver

import (
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/bwmarrin/snowflake"
	ut "github.com/go-playground/universal-translator"
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
	"golang.org/x/text/language"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/pkg/bininfo"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
	"github.com/penguin-statistics/backend-next/internal/utils/i18n"
)

func Create(config *config.Config, flake *snowflake.Node) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "Penguin Stats Backend v3",
		ServerHeader: fmt.Sprintf("Penguin/%s", bininfo.GitTag),
		// NOTICE: This will also affect WebSocket. Be aware if this fiber instance service is re-used
		//         for long connection services.
		ReadTimeout:  time.Second * 20,
		WriteTimeout: time.Second * 20,
		// allow possibility for graceful shutdown, otherwise app#Shutdown() will block forever
		IdleTimeout:             config.HttpServerShutdownTimeout,
		ProxyHeader:             fiber.HeaderXForwardedFor,
		EnableTrustedProxyCheck: true,
		TrustedProxies: []string{
			"::1",
			"127.0.0.1",
		},
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

			log.Error().
				Stack().
				Err(err).
				Str("method", ctx.Method()).
				Str("path", ctx.Path()).
				Int("status", code).
				Msg("Internal Server Error")

			return ctx.Status(code).JSON(fiber.Map{
				"code":    errors.CodeInternalError,
				"message": "internal server error",
			})
		},
	})

	app.Use(favicon.New())
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
		},
	))
	app.Use(helmet.New(helmet.Config{
		HSTSMaxAge:         31356000,
		HSTSPreloadEnabled: true,
		ReferrerPolicy:     "strict-origin-when-cross-origin",
		PermissionPolicy:   "interest-cohort=()",
		// ContentSecurityPolicy: "default-src 'none'; script-src 'none'; worker-src 'none'; frame-ancestors 'none'; sandbox",
	}))
	// app.Use("/api/v3/live", func(c *fiber.Ctx) error {
	// 	if websocket.IsWebSocketUpgrade(c) {
	// 		return c.Next()
	// 	}
	// 	return fiber.ErrUpgradeRequired
	// })
	app.Use(func(c *fiber.Ctx) error {
		set := func(trans ut.Translator) error {
			c.Locals("T", trans)
			return c.Next()
		}

		tags, _, err := language.ParseAcceptLanguage(c.Get(fiber.HeaderAcceptLanguage))
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
	app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
		StackTraceHandler: func(c *fiber.Ctx, e interface{}) {
			buf := make([]byte, 4096)
			buf = buf[:runtime.Stack(buf, false)]
			log.Error().Msgf("panic: %v\n%s\n", e, buf)
		},
	}))
	if config.TracingEnabled {
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
	if config.DevMode {
		log.Info().Msg("Running in DEV mode")
		app.Use(pprof.New())

		app.Use(logger.New(logger.Config{
			Format:     "${pid} ${locals:requestid} ${status} ${latency}\t${ip}\t- ${method} ${url}\n",
			TimeFormat: time.RFC3339,
			Output:     os.Stdout,
		}))
	} else {
		app.Use(logger.New(logger.Config{
			Format:     "${pid} ${locals:requestid} ${status} ${latency}\t${ip}\t- ${method} ${url}\n",
			TimeFormat: time.RFC3339,
			Output:     log.Logger,
		}))
	}

	return app
}
