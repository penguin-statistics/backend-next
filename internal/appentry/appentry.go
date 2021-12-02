package appentry

import (
	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/controllers"
	"github.com/penguin-statistics/backend-next/internal/controllers/shims"
	"github.com/penguin-statistics/backend-next/internal/infra"
	"github.com/penguin-statistics/backend-next/internal/server"
	httpserver "github.com/penguin-statistics/backend-next/internal/server/http"

	"go.uber.org/fx"
)

func ProvideOptions(includeSwagger bool) []fx.Option {
	opts := []fx.Option{
		fx.Provide(config.Parse),
		fx.Provide(httpserver.CreateServer),
		fx.Provide(infra.ProvidePostgres),
		fx.Provide(infra.ProvideRedis),
		fx.Provide(server.CreateVersioningEndpoints),
		fx.Invoke(shims.RegisterItemController),
		fx.Invoke(controllers.RegisterIndexController),
		fx.Invoke(controllers.RegisterItemController),
	}

	if includeSwagger {
		opts = append(opts, fx.Invoke(controllers.RegisterSwaggerController))
	}

	return opts
}
