package account

import "go.uber.org/fx"

func Module() fx.Option {
	return fx.Module("account",
		fx.Provide(
			NewRepo,
			NewService,
		),
		fx.Invoke(
			InitCache,
		),
	)
}
