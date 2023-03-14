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
		NewSnapshot,
		NewTimeRange,
		NewDropReport,
		NewRejectRule,
		NewDropPattern,
		NewDropReportExtra,
		NewDropMatrixElement,
		NewRecognitionDefect,
		NewDropPatternElement,
		NewPatternMatrixElement,
	))
}
