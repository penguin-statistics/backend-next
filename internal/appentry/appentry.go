package appentry

import (
	"time"

	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/config"
	controllermeta "github.com/penguin-statistics/backend-next/internal/controller/meta"
	controllerv2 "github.com/penguin-statistics/backend-next/internal/controller/v2"
	controllerv3 "github.com/penguin-statistics/backend-next/internal/controller/v3"
	"github.com/penguin-statistics/backend-next/internal/infra"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	"github.com/penguin-statistics/backend-next/internal/pkg/crypto"
	"github.com/penguin-statistics/backend-next/internal/pkg/flake"
	"github.com/penguin-statistics/backend-next/internal/pkg/logger"
	"github.com/penguin-statistics/backend-next/internal/pkg/observability"
	"github.com/penguin-statistics/backend-next/internal/repo"
	"github.com/penguin-statistics/backend-next/internal/server/httpserver"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/util/reportverifs"
	"github.com/penguin-statistics/backend-next/internal/workers/calcwkr"
	"github.com/penguin-statistics/backend-next/internal/workers/reportwkr"
)

func ProvideOptions(includeSwagger bool) []fx.Option {
	opts := []fx.Option{
		// Misc
		fx.Provide(config.Parse),
		fx.Provide(flake.New),
		fx.Provide(httpserver.Create),
		fx.Provide(svr.CreateEndpointGroups),
		fx.Provide(crypto.NewCrypto),

		// Infrastructures
		fx.Provide(
			infra.NATS,
			infra.Redis,
			infra.Postgres,
			infra.GeoIPDatabase,
		),

		// Verifiers
		fx.Provide(
			reportverifs.NewMD5Verifier,
			reportverifs.NewUserVerifier,
			reportverifs.NewDropVerifier,
			reportverifs.NewReportVerifier,
			reportverifs.NewRejectRuleVerifier,
		),

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
		fx.Invoke(observability.Launch),

		// Controllers (v2)
		controllerv2.Module(),

		// Controllers (v3)
		controllerv3.Module(),

		// Controllers (meta)
		controllermeta.Module(),

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

	if includeSwagger {
		opts = append(opts, fx.Invoke(controllermeta.RegisterSwagger))
	}

	return opts
}
