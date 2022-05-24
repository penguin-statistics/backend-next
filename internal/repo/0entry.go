package repo

import (
	"go.uber.org/fx"
)

func Module() fx.Option {
	opts := []fx.Option{
		fx.Provide(
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
			NewDropPatternElement,
			NewPatternMatrixElement,
		),
	}
	return fx.Module("repo", opts...)
}
