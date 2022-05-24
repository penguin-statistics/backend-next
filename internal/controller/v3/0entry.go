package v3

import (
	"go.uber.org/fx"
)

func Module() fx.Option {
	opts := []fx.Option{
		fx.Invoke(
			RegisterItem,
			RegisterLive,
			RegisterStage,
			RegisterZone,
		),
	}
	return fx.Module("controllers.v3", opts...)
}
