package service

import (
	"go.uber.org/fx"
)

func Module() fx.Option {
	opts := []fx.Option{
		fx.Provide(
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
		),
	}
	return fx.Module("service", opts...)
}
