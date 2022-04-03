package appentry

import (
	"time"

	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/config"
	controllerv2 "github.com/penguin-statistics/backend-next/internal/controller/v2"
	controllermeta "github.com/penguin-statistics/backend-next/internal/controller/meta"
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
		fx.Provide(flake.NewSnowflake),
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
		fx.Provide(repo.NewStageRepo),
		fx.Provide(repo.NewNotice),
		fx.Provide(repo.NewActivity),
		fx.Provide(repo.NewAccount),
		fx.Provide(repo.NewDropInfo),
		fx.Provide(repo.NewPropertyRepo),
		fx.Provide(repo.NewTimeRange),
		fx.Provide(repo.NewDropReport),
		fx.Provide(repo.NewDropPattern),
		fx.Provide(repo.NewTrendElement),
		fx.Provide(repo.NewDropReportExtra),
		fx.Provide(repo.NewDropMatrixElement),
		fx.Provide(repo.NewDropPatternElement),
		fx.Provide(repo.NewPatternMatrixElement),
		fx.Provide(repo.NewAdmin),
		fx.Provide(service.NewItemService),
		fx.Provide(service.NewZoneService),
		fx.Provide(service.NewStageService),
		fx.Provide(service.NewGeoIPService),
		fx.Provide(service.NewTrendService),
		fx.Provide(service.NewAdminService),
		fx.Provide(service.NewHealthService),
		fx.Provide(service.NewNoticeService),
		fx.Provide(service.NewReportService),
		fx.Provide(service.NewAccountService),
		fx.Provide(service.NewFormulaService),
		fx.Provide(service.NewActivityService),
		fx.Provide(service.NewDropInfoService),
		fx.Provide(service.NewShortURLService),
		fx.Provide(service.NewTimeRangeService),
		fx.Provide(service.NewSiteStatsService),
		fx.Provide(service.NewDropMatrixService),
		fx.Provide(service.NewDropReportService),
		fx.Provide(service.NewTrendElementService),
		fx.Provide(service.NewPatternMatrixService),
		fx.Provide(service.NewDropMatrixElementService),
		fx.Provide(service.NewDropPatternElementService),
		fx.Provide(service.NewPatternMatrixElementService),
		fx.Provide(svr.CreateVersioningEndpoints),
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
		fx.Invoke(controllerv2.RegisterShortURLController),
		fx.Invoke(controllermeta.RegisterMetaController),
		fx.Invoke(controllermeta.RegisterIndexController),
		fx.Invoke(controllermeta.RegisterAdminController),
		fx.Invoke(calcwkr.Start),
		fx.StartTimeout(1 * time.Second),
		// StopTimeout is not typically needed, since we're using fiber's Shutdown(),
		// in which fiber has its own IdleTimeout for controlling the shutdown timeout.
		// It acts as a countermeasure in case the fiber app is not properly shutting down.
		fx.StopTimeout(5 * time.Minute),
	}

	if includeSwagger {
		opts = append(opts, fx.Invoke(controllermeta.RegisterSwaggerController))
	}

	return opts
}
