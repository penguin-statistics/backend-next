package service

import (
	"go.uber.org/fx"
)

func Module() fx.Option {
	return fx.Module("service", fx.Provide(
		NewItem,
		NewZone,
		NewStage,
		NewGeoIP,
		NewTrend,
		NewAdmin,
		NewHealth,
		NewNotice,
		NewReport,
		NewAccount,
		NewFormula,
		NewFrontendConfig,
		NewActivity,
		NewDropInfo,
		NewShortURL,
		NewTimeRange,
		NewSiteStats,
		NewDropMatrix,
		NewDropReport,
		NewTrendElement,
		NewPatternMatrix,
		NewDropMatrixElement,
		NewDropPatternElement,
		NewPatternMatrixElement,
	))
}
