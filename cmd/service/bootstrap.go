package main

import (
	"context"
	"penguin-stats-v4/internal/appentry"

	"go.uber.org/fx"
)

func main() {
	opts := []fx.Option{}
	opts = append(opts, appentry.ProvideOptions()...)
	opts = append(opts, fx.Invoke(run))

	app := fx.New(opts...)

	ctx := context.Background()
	err := app.Start(ctx)
	if err != nil {
		panic(err)
	}
}
