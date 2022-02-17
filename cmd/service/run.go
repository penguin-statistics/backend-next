package service

import (
	"context"
	"net"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx"

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
				if err := app.Listener(ln); err != nil {
					log.Error().Err(err).Msg("server terminated unexpectedly")
				}
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
