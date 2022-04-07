package admin

import "go.uber.org/fx"

func Module() fx.Option {
	return fx.Module("admin",
		fx.Provide(
			NewRepo,
			NewService,
		),
	)
}
