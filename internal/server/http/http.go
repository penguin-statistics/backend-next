package httpserver

import (
	"os"
	"time"

	"penguin-stats-v4/internal/config"
	"penguin-stats-v4/internal/pkg/errors"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/favicon"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/pprof"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/gofiber/helmet/v2"
)

func CreateServer(config *config.Config) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "Penguin Stats Backend v4",
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
				// Override status code if errors.PenguinError type
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
				"error": err.Error(),
			})
		},
	})

	app.Use(favicon.New())
	app.Use(requestid.New())
	app.Use(logger.New(logger.Config{
		Format:     "${pid} ${ip} ${locals:requestid} ${status} ${latency} - ${method} ${path}\n",
		TimeFormat: time.RFC3339,
		Output:     os.Stdout,
	}))
	app.Use(cors.New())
	app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
	}))
	app.Use(helmet.New(helmet.Config{
		HSTSMaxAge:            31356000,
		HSTSPreloadEnabled:    true,
		ReferrerPolicy:        "strict-origin-when-cross-origin",
		ContentSecurityPolicy: "default-src 'none'; script-src 'none'; worker-src 'none'; frame-ancestors 'none'; sandbox",
		PermissionPolicy:      "interest-cohort=()",
	}))

	if config.DevMode {
		app.Use(pprof.New())
	}

	return app
}
