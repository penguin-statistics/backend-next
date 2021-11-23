package service

import (
	"context"
	"penguin-stats-v4/internal/config"
	httpserver "penguin-stats-v4/internal/server/http"

	"go.uber.org/fx"
)

func Bootstrap() {
	app := fx.New(
		fx.Provide(config.Parse),
		fx.Provide(httpserver.CreateServer),
		fx.Invoke(run),
	)

	ctx := context.Background()
	err := app.Start(ctx)
	if err != nil {
		panic(err)
	}
}
