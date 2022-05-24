package v2

import (
	"go.uber.org/fx"
)

func Module() fx.Option {
	opts := []fx.Option{
		fx.Invoke(
			RegisterItem,
			RegisterZone,
			RegisterStage,
			RegisterNotice,
			RegisterResult,
			RegisterReport,
			RegisterAccount,
			RegisterFormula,
			RegisterPrivate,
			RegisterSiteStats,
			RegisterEventPeriod,
			RegisterShortURL,
		),
	}
	return fx.Module("controllers.v2", opts...)
}
