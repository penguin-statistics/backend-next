package main

import (
	"context"

	"github.com/penguin-statistics/backend-next/internal/appentry"

	"go.uber.org/fx"
)

func populate(targets ...interface{}) {
	// for testing, logger is too annoying. therefore we use a NopLogger here
	opts := []fx.Option{fx.NopLogger}
	opts = append(opts, appentry.ProvideOptions(false)...)
	opts = append(opts, fx.Populate(targets...))

	app := fx.New(
		opts...,
	)

	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
