package appentry

import (
	"os"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/controllers"
	"github.com/penguin-statistics/backend-next/internal/controllers/shims"
	"github.com/penguin-statistics/backend-next/internal/infra"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/pkg/flake"
	"github.com/penguin-statistics/backend-next/internal/pkg/logger"
	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/server/httpserver"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/utils"
	"github.com/penguin-statistics/backend-next/internal/utils/reportutils"
)

func ProvideOptions(includeSwagger bool) []fx.Option {
	opts := []fx.Option{
		fx.Provide(config.Parse),
		fx.Provide(flake.NewSnowflake),
		fx.Provide(httpserver.Create),
		fx.Provide(infra.ProvideNats),
		fx.Provide(infra.ProvideRedis),
		fx.Provide(infra.ProvidePostgres),
		fx.Provide(infra.ProvideGeoIPDatabase),
		fx.Provide(reportutils.NewMD5Verifier),
		fx.Provide(reportutils.NewUserVerifier),
		fx.Provide(reportutils.NewDropVerifier),
		fx.Provide(reportutils.NewReportVerifier),
		fx.Provide(repos.NewItemRepo),
		fx.Provide(repos.NewZoneRepo),
		fx.Provide(repos.NewStageRepo),
		fx.Provide(repos.NewNoticeRepo),
		fx.Provide(repos.NewActivityRepo),
		fx.Provide(repos.NewAccountRepo),
		fx.Provide(repos.NewDropInfoRepo),
		fx.Provide(repos.NewPropertyRepo),
		fx.Provide(repos.NewTimeRangeRepo),
		fx.Provide(repos.NewDropReportRepo),
		fx.Provide(repos.NewDropPatternRepo),
		fx.Provide(repos.NewTrendElementRepo),
		fx.Provide(repos.NewDropReportExtraRepo),
		fx.Provide(repos.NewDropMatrixElementRepo),
		fx.Provide(repos.NewDropPatternElementRepo),
		fx.Provide(repos.NewPatternMatrixElementRepo),
		fx.Provide(service.NewItemService),
		fx.Provide(service.NewStageService),
		fx.Provide(service.NewZoneService),
		fx.Provide(service.NewGeoIPService),
		fx.Provide(service.NewTrendService),
		fx.Provide(service.NewReportService),
		fx.Provide(service.NewNoticeService),
		fx.Provide(service.NewActivityService),
		fx.Provide(service.NewAccountService),
		fx.Provide(service.NewFormulaService),
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
		fx.Provide(server.CreateVersioningEndpoints),
		fx.Provide(utils.NewCrypto),
		fx.Invoke(cache.Initialize),
		fx.Invoke(logger.Configure),
		fx.Invoke(shims.RegisterItemController),
		fx.Invoke(shims.RegisterZoneController),
		fx.Invoke(shims.RegisterStageController),
		fx.Invoke(shims.RegisterNoticeController),
		fx.Invoke(shims.RegisterResultController),
		fx.Invoke(shims.RegisterReportController),
		fx.Invoke(shims.RegisterAccountController),
		fx.Invoke(shims.RegisterFormulaController),
		fx.Invoke(shims.RegisterPrivateController),
		fx.Invoke(shims.RegisterSiteStatsController),
		fx.Invoke(shims.RegisterEventPeriodController),
		fx.Invoke(shims.RegisterTestController),
		fx.Invoke(controllers.RegisterMetaController),
		fx.StartTimeout(1 * time.Second),
		// StopTimeout is not typically needed, since we're using fiber's Shutdown(),
		// in which fiber has its own IdleTimeout for controlling the shutdown timeout.
		// It acts as a countermeasure in case the fiber app is not properly shutting down.
		fx.StopTimeout(5 * time.Minute),
		fx.WithLogger(func(config *config.Config) fxevent.Logger {
			if config.DevMode {
				return &fxevent.ConsoleLogger{W: os.Stdout}
			} else {
				return fxevent.NopLogger
			}
		}),
	}

	if includeSwagger {
		opts = append(opts, fx.Invoke(controllers.RegisterSwaggerController))
	}

	return opts
}
