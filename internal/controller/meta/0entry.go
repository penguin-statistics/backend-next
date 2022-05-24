package meta

import (
	"go.uber.org/fx"
)

func Module() fx.Option {
	opts := []fx.Option{
		fx.Invoke(
			RegisterMeta,
			RegisterIndex,
			RegisterAdmin,
		),
	}
	return fx.Module("controllers.meta", opts...)
}
