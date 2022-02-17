package service

import (
	"context"
	"net"

	"go.uber.org/fx"
	// "github.com/davecgh/go-spew/spew"
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/config"
)

func run(app *fiber.App, config *config.Config, lc fx.Lifecycle) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			ln, err := net.Listen("tcp", config.Address)
			if err != nil {
				return err
			}

			go func() {
				app.Listener(ln)
			}()

			return nil
		},
		OnStop: func(ctx context.Context) error {
			if config.DevMode {
				return nil
			}
			return app.Shutdown()
		},
	})
}
