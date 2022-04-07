package activity

import "go.uber.org/fx"

func Module() fx.Option {
	return fx.Module("activity",
		fx.Provide(
			NewRepo,
			NewService,
		),
		fx.Invoke(
			InitCache,
		),
	)
}
