package appentry

import (
	"github.com/penguin-statistics/backend-next/internal/workers/reportwkr"
	"time"

	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/config"
	controllermeta "github.com/penguin-statistics/backend-next/internal/controller/meta"
	controllerv2 "github.com/penguin-statistics/backend-next/internal/controller/v2"
	"github.com/penguin-statistics/backend-next/internal/infra"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	"github.com/penguin-statistics/backend-next/internal/pkg/crypto"
	"github.com/penguin-statistics/backend-next/internal/pkg/flake"
	"github.com/penguin-statistics/backend-next/internal/pkg/logger"
	"github.com/penguin-statistics/backend-next/internal/repo"
	"github.com/penguin-statistics/backend-next/internal/server/httpserver"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/util/reportutil"
	"github.com/penguin-statistics/backend-next/internal/workers/calcwkr"
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
			reportutil.NewMD5Verifier,
			reportutil.NewUserVerifier,
			reportutil.NewDropVerifier,
			reportutil.NewReportVerifier,
		),

		// Repositories
		fx.Provide(
			repo.NewItem,
			repo.NewZone,
			repo.NewAdmin,
			repo.NewStage,
			repo.NewNotice,
			repo.NewAccount,
			repo.NewActivity,
			repo.NewDropInfo,
			repo.NewProperty,
			repo.NewTimeRange,
			repo.NewDropReport,
			repo.NewDropPattern,
			repo.NewTrendElement,
			repo.NewDropReportExtra,
			repo.NewDropMatrixElement,
			repo.NewDropPatternElement,
			repo.NewPatternMatrixElement,
		),

		// Services
		fx.Provide(
			service.NewItem,
			service.NewZone,
			service.NewStage,
			service.NewGeoIP,
			service.NewTrend,
			service.NewAdmin,
			service.NewHealth,
			service.NewNotice,
			service.NewReport,
			service.NewAccount,
			service.NewFormula,
			service.NewActivity,
			service.NewDropInfo,
			service.NewShortURL,
			service.NewTimeRange,
			service.NewSiteStats,
			service.NewDropMatrix,
			service.NewDropReport,
			service.NewTrendElement,
			service.NewPatternMatrix,
			service.NewDropMatrixElement,
			service.NewDropPatternElement,
			service.NewPatternMatrixElement,
		),

		// Global Singleton Inits
		fx.Invoke(logger.Configure),
		fx.Invoke(infra.SentryInit),
		fx.Invoke(cache.Initialize),

		// Controllers (v2)
		fx.Invoke(
			controllerv2.RegisterItem,
			controllerv2.RegisterZone,
			controllerv2.RegisterStage,
			controllerv2.RegisterNotice,
			controllerv2.RegisterResult,
			controllerv2.RegisterReport,
			controllerv2.RegisterAccount,
			controllerv2.RegisterFormula,
			controllerv2.RegisterPrivate,
			controllerv2.RegisterSiteStats,
			controllerv2.RegisterEventPeriod,
			controllerv2.RegisterShortURL,
		),

		// Controllers (meta)
		fx.Invoke(
			controllermeta.RegisterMeta,
			controllermeta.RegisterIndex,
			controllermeta.RegisterAdmin,
		),

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
