package reportverifs

import "go.uber.org/fx"

func Module() fx.Option {
	return fx.Module("reportverifs", fx.Provide(
		NewDropVerifier,
		NewReportVerifier,
		NewRejectRuleVerifier,
	))
}
