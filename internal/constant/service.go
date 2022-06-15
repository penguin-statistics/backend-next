package constant

const (
	FormulaPropertyKey = "formula"

	FrontendConfigPropertyKey = "frontend_config"

	// SlimHeaderKey is to indicate whether the current request shall be ignored by Sentry transaction tracing.
	// This is typically used by probes to avoid useless data being sent to Sentry.
	SlimHeaderKey = "X-Slim"
)
