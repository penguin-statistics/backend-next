package infra

import (
	"github.com/getsentry/sentry-go"
	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/pkg/bininfo"
)

// SentryInit initializes sentry with side-effect
func SentryInit(config *config.Config) error {
	if config.SentryDSN == "" {
		log.Warn().Msg("Sentry is disabled due to missing DSN.")
		return nil
	} else {
		log.Info().Msg("Initializing Sentry...")
	}
	return sentry.Init(sentry.ClientOptions{
		Dsn:              config.SentryDSN,
		Release:          "backend-next@" + bininfo.Version,
		Debug:            config.DevMode,
		AttachStacktrace: true,
		TracesSampleRate: 0.01,
	})
}
