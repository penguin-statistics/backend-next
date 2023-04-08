package service

import (
	"go.uber.org/fx"
)

func Module() fx.Option {
	return fx.Module("service", fx.Provide(
		NewItem,
		NewInit,
		NewZone,
		NewStage,
		NewGeoIP,
		NewTrend,
		NewAdmin,
		NewUpyun,
		NewHealth,
		NewNotice,
		NewReport,
		NewAccount,
		NewFormula,
		NewActivity,
		NewDropInfo,
		NewShortURL,
		NewSnapshot,
		NewAnalytics,
		NewLiveHouse,
		NewSiteStats,
		NewTimeRange,
		NewDropMatrix,
		NewDropReport,
		NewPatternMatrix,
		NewFrontendConfig,
		NewDropMatrixElement,
		NewDropPatternElement,
		NewPatternMatrixElement,
		NewExport,
	))
}
