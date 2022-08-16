package v3

import (
	"go.uber.org/fx"
)

func Module() fx.Option {
	return fx.Module("controllers.v3", fx.Invoke(
		RegisterItem,
		RegisterLive,
		RegisterStage,
		RegisterZone,
		RegisterDataset,
		RegisterInit,
	))
}
