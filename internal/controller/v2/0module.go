package v2

import (
	"go.uber.org/fx"
)

func Module() fx.Option {
	return fx.Module("controller.v2", fx.Invoke(
		RegisterItem,
		RegisterZone,
		RegisterStage,
		RegisterNotice,
		RegisterResult,
		RegisterReport,
		RegisterAccount,
		RegisterFormula,
		RegisterFrontendConfig,
		RegisterPrivate,
		RegisterSiteStats,
		RegisterEventPeriod,
		RegisterShortURL,
	))
}
