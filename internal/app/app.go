package app

import (
	"time"

	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/app/appcontext"
	"exusiai.dev/backend-next/internal/controller"
	"exusiai.dev/backend-next/internal/infra"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/pkg/crypto"
	"exusiai.dev/backend-next/internal/pkg/logger"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/server"
	"exusiai.dev/backend-next/internal/service"
	"exusiai.dev/backend-next/internal/util/reportverifs"
	"exusiai.dev/backend-next/internal/workers/calcwkr"
	"exusiai.dev/backend-next/internal/workers/reportwkr"
)

func Options(ctx appcontext.Ctx, additionalOpts ...fx.Option) []fx.Option {
	conf, err := appconfig.Parse(ctx)
	if err != nil {
		panic(err)
	}

	// logger and configuration are the only two things that are not in the fx graph
	// because some other packages need them to be initialized before fx starts
	logger.Configure(conf)

	baseOpts := []fx.Option{
		// fx meta
		fx.WithLogger(logger.Fx),

		// Misc
		fx.Supply(conf),
		fx.Provide(crypto.NewCrypto),

		// Infrastructures
		infra.Module(),

		// Servers
		server.Module(),

		// Verifiers
		reportverifs.Module(),

		// Repositories
		repo.Module(),

		// Services
		service.Module(),

		// Global Singleton Inits: Keep those before controllers to ensure they are initialized
		// before controllers are registered as controllers are also fx#Invoke functions which
		// are called in the order of their registration.
		fx.Invoke(infra.SentryInit),
		fx.Invoke(cache.Initialize),

		// Controllers
		controller.Module(controller.OptIncludeSwagger),

		// Workers
		fx.Invoke(calcwkr.Start),
		fx.Invoke(reportwkr.Start),

		// fx Extra Options
		fx.StartTimeout(1 * time.Second),
		// StopTimeout is not typically needed, since we're using fiber's Shutdown(),
		// in which fiber has its own IdleTimeout for controlling the shutdown timeout.
		// It acts as a countermeasure in case the fiber app is not properly shutting down.
		fx.StopTimeout(5 * time.Minute),
	}

	return append(baseOpts, additionalOpts...)
}

func New(ctx appcontext.Ctx, additionalOpts ...fx.Option) *fx.App {
	return fx.New(Options(ctx, additionalOpts...)...)
}
