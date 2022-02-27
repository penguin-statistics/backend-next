package infra

import (
	"github.com/getsentry/sentry-go"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/pkg/bininfo"
)

// SentryInit initializes sentry with side-effect
func SentryInit(config *config.Config) error {
	return sentry.Init(sentry.ClientOptions{
		Dsn:              "https://bd66e3da75eb4510a381e30e1e71f6fa@o292786.ingest.sentry.io/6233653",
		Release:          "backend-next@" + bininfo.Version,
		Debug:            config.DevMode,
		AttachStacktrace: true,
	})
}
