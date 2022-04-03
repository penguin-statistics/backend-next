package appentry

import (
	"time"

	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/controller"
	controllerv2 "github.com/penguin-statistics/backend-next/internal/controller/v2"
	"github.com/penguin-statistics/backend-next/internal/infra"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/pkg/flake"
	"github.com/penguin-statistics/backend-next/internal/pkg/logger"
	"github.com/penguin-statistics/backend-next/internal/repo"
	"github.com/penguin-statistics/backend-next/internal/server/httpserver"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/utils"
	"github.com/penguin-statistics/backend-next/internal/utils/reportutils"
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
		fx.Provide(reportutils.NewMD5Verifier),
		fx.Provide(reportutils.NewUserVerifier),
		fx.Provide(reportutils.NewDropVerifier),
		fx.Provide(reportutils.NewReportVerifier),
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
		fx.Provide(utils.NewCrypto),
		fx.Invoke(logger.Configure),
		fx.Invoke(infra.SentryInit),
		fx.Invoke(cache.Initialize),
		fx.Invoke(controllerv2.RegisterItemController),
		fx.Invoke(controllerv2.RegisterZoneController),
		fx.Invoke(controllerv2.RegisterStageController),
		fx.Invoke(controllerv2.RegisterNoticeController),
		fx.Invoke(controllerv2.RegisterResultController),
		fx.Invoke(controllerv2.RegisterReportController),
		fx.Invoke(controllerv2.RegisterAccountController),
		fx.Invoke(controllerv2.RegisterFormulaController),
		fx.Invoke(controllerv2.RegisterPrivateController),
		fx.Invoke(controllerv2.RegisterSiteStatsController),
		fx.Invoke(controllerv2.RegisterEventPeriodController),
		fx.Invoke(controller.RegisterMetaController),
		fx.Invoke(controller.RegisterIndexController),
		fx.Invoke(controller.RegisterAdminController),
		fx.Invoke(controller.RegisterShortURLController),
		fx.Invoke(calcwkr.Start),
		fx.StartTimeout(1 * time.Second),
		// StopTimeout is not typically needed, since we're using fiber's Shutdown(),
		// in which fiber has its own IdleTimeout for controlling the shutdown timeout.
		// It acts as a countermeasure in case the fiber app is not properly shutting down.
		fx.StopTimeout(5 * time.Minute),
	}

	if includeSwagger {
		opts = append(opts, fx.Invoke(controller.RegisterSwaggerController))
	}

	return opts
}
