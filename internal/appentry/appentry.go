package appentry

import (
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
		fx.Provide(config.Parse),
		fx.Provide(flake.New),
		fx.Provide(httpserver.Create),
		fx.Provide(infra.NATS),
		fx.Provide(infra.Redis),
		fx.Provide(infra.Postgres),
		fx.Provide(infra.GeoIPDatabase),
		fx.Provide(reportutil.NewMD5Verifier),
		fx.Provide(reportutil.NewUserVerifier),
		fx.Provide(reportutil.NewDropVerifier),
		fx.Provide(reportutil.NewReportVerifier),
		fx.Provide(repo.NewItem),
		fx.Provide(repo.NewZone),
		fx.Provide(repo.NewAdmin),
		fx.Provide(repo.NewStage),
		fx.Provide(repo.NewNotice),
		fx.Provide(repo.NewAccount),
		fx.Provide(repo.NewActivity),
		fx.Provide(repo.NewDropInfo),
		fx.Provide(repo.NewProperty),
		fx.Provide(repo.NewTimeRange),
		fx.Provide(repo.NewDropReport),
		fx.Provide(repo.NewDropPattern),
		fx.Provide(repo.NewTrendElement),
		fx.Provide(repo.NewDropReportExtra),
		fx.Provide(repo.NewDropMatrixElement),
		fx.Provide(repo.NewDropPatternElement),
		fx.Provide(repo.NewPatternMatrixElement),
		fx.Provide(service.NewItem),
		fx.Provide(service.NewZone),
		fx.Provide(service.NewStage),
		fx.Provide(service.NewGeoIP),
		fx.Provide(service.NewTrend),
		fx.Provide(service.NewAdmin),
		fx.Provide(service.NewHealth),
		fx.Provide(service.NewNotice),
		fx.Provide(service.NewReport),
		fx.Provide(service.NewAccount),
		fx.Provide(service.NewFormula),
		fx.Provide(service.NewActivity),
		fx.Provide(service.NewDropInfo),
		fx.Provide(service.NewShortURL),
		fx.Provide(service.NewTimeRange),
		fx.Provide(service.NewSiteStats),
		fx.Provide(service.NewDropMatrix),
		fx.Provide(service.NewDropReport),
		fx.Provide(service.NewTrendElement),
		fx.Provide(service.NewPatternMatrix),
		fx.Provide(service.NewDropMatrixElement),
		fx.Provide(service.NewDropPatternElement),
		fx.Provide(service.NewPatternMatrixElement),
		fx.Provide(svr.CreateEndpointGroups),
		fx.Provide(crypto.NewCrypto),
		fx.Invoke(logger.Configure),
		fx.Invoke(infra.SentryInit),
		fx.Invoke(cache.Initialize),
		fx.Invoke(controllerv2.RegisterItem),
		fx.Invoke(controllerv2.RegisterZone),
		fx.Invoke(controllerv2.RegisterStage),
		fx.Invoke(controllerv2.RegisterNotice),
		fx.Invoke(controllerv2.RegisterResult),
		fx.Invoke(controllerv2.RegisterReport),
		fx.Invoke(controllerv2.RegisterAccount),
		fx.Invoke(controllerv2.RegisterFormula),
		fx.Invoke(controllerv2.RegisterPrivate),
		fx.Invoke(controllerv2.RegisterSiteStats),
		fx.Invoke(controllerv2.RegisterEventPeriod),
		fx.Invoke(controllerv2.RegisterShortURL),
		fx.Invoke(controllermeta.RegisterMeta),
		fx.Invoke(controllermeta.RegisterIndex),
		fx.Invoke(controllermeta.RegisterAdmin),
		fx.Invoke(calcwkr.Start),
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
