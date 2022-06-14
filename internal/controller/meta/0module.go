package meta

import (
	"go.uber.org/fx"
)

func Module() fx.Option {
	return fx.Module("controllers.meta", fx.Invoke(
		RegisterMeta,
		RegisterIndex,
		RegisterAdmin,
	))
}
