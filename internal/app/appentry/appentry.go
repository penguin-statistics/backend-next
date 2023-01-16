package appentry

import (
	"time"

	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/controller"
	"exusiai.dev/backend-next/internal/infra"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/pkg/crypto"
	"exusiai.dev/backend-next/internal/pkg/logger"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/server/httpserver"
	"exusiai.dev/backend-next/internal/server/svr"
	"exusiai.dev/backend-next/internal/service"
	"exusiai.dev/backend-next/internal/util/reportverifs"
	"exusiai.dev/backend-next/internal/workers/calcwkr"
	"exusiai.dev/backend-next/internal/workers/reportwkr"
)

func ProvideOptions() []fx.Option {
	opts := []fx.Option{
		// Misc
		fx.Provide(appconfig.Parse),
		fx.Provide(httpserver.Create),
		fx.Provide(svr.CreateEndpointGroups),
		fx.Provide(crypto.NewCrypto),

		// Infrastructures
		infra.Module(),

		// Verifiers
		reportverifs.Module(),

		// Repositories
		repo.Module(),

		// Services
		service.Module(),

		// Global Singleton Inits: Keep those before controllers to ensure they are initialized
		// before controllers are registered as controllers are also fx#Invoke functions which
		// are called in the order of their registration.
		fx.Invoke(logger.Configure),
		fx.Invoke(infra.SentryInit),
		fx.Invoke(cache.Initialize),
		fx.WithLogger(logger.Fx),

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

	return opts
}
