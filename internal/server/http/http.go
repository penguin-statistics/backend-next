package httpserver

import (
	"os"
	"time"

	"penguin-stats-v4/internal/config"

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
		ServerHeader: "Penguin/1.0",
		// NOTICE: This will also affect WebSocket. Be aware if this fiber instance service is re-used.
		ReadTimeout:  time.Second * 20,
		WriteTimeout: time.Second * 20,
		// allow possibility for graceful shutdown, otherwise app#Shutdown() will block forever
		IdleTimeout: time.Second * 600,
		ProxyHeader: fiber.HeaderXForwardedFor,
	})

	app.Use(favicon.New())
	app.Use(requestid.New())
	app.Use(logger.New(logger.Config{
		Format:     "${pid} ${ip} ${locals:requestid} ${status} ${latency} - ${method} ${path}\n",
		TimeFormat: time.RFC3339,
		Output:     os.Stdout,
	}))
	app.Use(cors.New())
	app.Use(recover.New())
	app.Use(helmet.New(helmet.Config{
		HSTSMaxAge:            31356000,
		HSTSPreloadEnabled:    true,
		ReferrerPolicy:        "strict-origin-when-cross-origin",
		ContentSecurityPolicy: "default-src 'none'; script-src 'none'; worker-src 'none'; frame-ancestors 'none'; sandbox",
	}))

	if config.DevMode {
		app.Use(pprof.New())
	}

	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("Hello, World!")
	})

	app.Get("/_teapot", func(c *fiber.Ctx) error {
		return c.SendStatus(418)
	})

	return app
}
