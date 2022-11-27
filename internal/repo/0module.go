package repo

import (
	"go.uber.org/fx"
)

func Module() fx.Option {
	return fx.Module("repo", fx.Provide(
		NewItem,
		NewZone,
		NewAdmin,
		NewStage,
		NewNotice,
		NewAccount,
		NewActivity,
		NewDropInfo,
		NewProperty,
		NewTimeRange,
		NewDropReport,
		NewRejectRule,
		NewDropPattern,
		NewTrendElement,
		NewDropReportExtra,
		NewDropMatrixElement,
		NewRecognitionDefect,
		NewDropPatternElement,
		NewPatternMatrixElement,
	))
}
