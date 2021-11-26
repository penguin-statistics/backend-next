package service

import (
	"context"
	"penguin-stats-v4/internal/config"
	"penguin-stats-v4/internal/controllers"
	"penguin-stats-v4/internal/controllers/shims"
	"penguin-stats-v4/internal/infra"
	"penguin-stats-v4/internal/server"
	httpserver "penguin-stats-v4/internal/server/http"

	"go.uber.org/fx"
)

func Bootstrap() {
	app := fx.New(
		fx.Provide(config.Parse),
		fx.Provide(httpserver.CreateServer),
		fx.Provide(infra.ProvidePostgres),
		fx.Provide(infra.ProvideRedis),
		fx.Provide(server.CreateVersioningEndpoints),
		fx.Invoke(shims.RegisterItemController),
		fx.Invoke(controllers.RegisterIndexController),
		fx.Invoke(controllers.RegisterItemController),
		fx.Invoke(run),
	)

	ctx := context.Background()
	err := app.Start(ctx)
	if err != nil {
		panic(err)
	}
}
