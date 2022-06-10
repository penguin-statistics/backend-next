package meta

import (
	"go.uber.org/fx"
)

func Module() fx.Option {
	return fx.Module("controller.meta", fx.Invoke(
		RegisterMeta,
		RegisterIndex,
		RegisterAdmin,
	))
}
