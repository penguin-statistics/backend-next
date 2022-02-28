package infra

import (
	"github.com/getsentry/sentry-go"
	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/pkg/bininfo"
)

// SentryInit initializes sentry with side-effect
func SentryInit(conf *config.Config) error {
	if conf.SentryDSN == "" {
		log.Warn().Msg("Sentry is disabled due to missing DSN.")
		return nil
	} else {
		log.Info().Msg("Initializing Sentry...")
	}
	return sentry.Init(sentry.ClientOptions{
		Dsn:              conf.SentryDSN,
		Release:          "backend-next@" + bininfo.Version,
		Debug:            conf.DevMode,
		AttachStacktrace: true,
		TracesSampleRate: 0.01,
	})
}
