package reportverifs

import "go.uber.org/fx"

func Module() fx.Option {
	return fx.Module("reportverifs", fx.Provide(
		NewMD5Verifier,
		NewUserVerifier,
		NewDropVerifier,
		NewReportVerifier,
		NewRejectRuleVerifier,
	))
}
