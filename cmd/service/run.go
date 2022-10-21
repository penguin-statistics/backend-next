package service

import (
	"context"
	"net"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gofiber/fiber/v2"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/config"
	"exusiai.dev/backend-next/internal/pkg/async"
	"exusiai.dev/backend-next/internal/server/httpserver"
)

func run(serviceApp *fiber.App, devOpsApp httpserver.DevOpsApp, conf *config.Config, lc fx.Lifecycle) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			serviceLn, err := net.Listen("tcp", conf.ServiceAddress)
			if err != nil {
				return err
			}

			go func() {
				if err := serviceApp.Listener(serviceLn); err != nil {
					log.Error().Err(err).Msg("server terminated unexpectedly")
				}
			}()

			if conf.DevOpsAddress == "" {
				log.Info().
					Str("evt.name", "infra.devops.disabled").
					Msg("DevOps server is disabled")
			} else {
				devOpsLn, err := net.Listen("tcp", conf.DevOpsAddress)
				if err != nil {
					return err
				}

				go func() {
					if err := devOpsApp.Listener(devOpsLn); err != nil {
						log.Error().Err(err).Msg("server terminated unexpectedly")
					}
				}()
			}

			return nil
		},
		OnStop: func(ctx context.Context) error {
			if conf.DevMode {
				return nil
			}

			return async.WaitAll(
				async.Errable(serviceApp.Shutdown),
				async.Errable(devOpsApp.Shutdown),
				async.Errable(func() error {
					flushed := sentry.Flush(time.Second * 30)
					if !flushed {
						return errors.New("sentry flush timeout, some events may be lost")
					}
					return nil
				}),
			)
		},
	})
}
