package appentry

import (
	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/controllers"
	"github.com/penguin-statistics/backend-next/internal/controllers/shims"
	"github.com/penguin-statistics/backend-next/internal/infra"
	"github.com/penguin-statistics/backend-next/internal/pkg/flake"
	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/server"
	httpserver "github.com/penguin-statistics/backend-next/internal/server/http"

	"go.uber.org/fx"
)

func ProvideOptions(includeSwagger bool) []fx.Option {
	opts := []fx.Option{
		fx.Provide(config.Parse),
		fx.Provide(flake.NewSnowflake),
		fx.Provide(httpserver.CreateServer),
		fx.Provide(infra.ProvideNats),
		fx.Provide(infra.ProvidePostgres),
		fx.Provide(infra.ProvideRedis),
		fx.Provide(repos.NewItemRepo),
		fx.Provide(repos.NewStageRepo),
		fx.Provide(repos.NewZoneRepo),
		fx.Provide(server.CreateVersioningEndpoints),
		fx.Invoke(shims.RegisterItemController),
		fx.Invoke(shims.RegisterStageController),
		fx.Invoke(shims.RegisterZoneController),
		fx.Invoke(controllers.RegisterLiveController),
		fx.Invoke(controllers.RegisterIndexController),
		fx.Invoke(controllers.RegisterItemController),
		fx.Invoke(controllers.RegisterStageController),
		fx.Invoke(controllers.RegisterZoneController),
	}

	if includeSwagger {
		opts = append(opts, fx.Invoke(controllers.RegisterSwaggerController))
	}

	return opts
}
