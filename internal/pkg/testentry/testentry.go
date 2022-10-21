package testentry

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/appentry"
)

func Populate(t zerolog.TestingLog, targets ...any) {
	// for testing, logger is too annoying. therefore, we use a NopLogger here
	opts := []fx.Option{fx.NopLogger}
	opts = append(opts, appentry.ProvideOptions(false)...)
	opts = append(opts, fx.Populate(targets...))
	opts = append(opts, fx.Invoke(func() {
		log.Logger = log.Logger.Output(zerolog.NewTestWriter(t))
	}))

	app := fx.New(
		opts...,
	)

	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
