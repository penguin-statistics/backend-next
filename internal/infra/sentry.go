package infra

import (
	"github.com/getsentry/sentry-go"
	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/pkg/bininfo"
)

func getEnvironment(conf *config.Config) string {
	if conf.DevMode {
		return "dev"
	} else {
		return "prod"
	}
}

// SentryInit initializes sentry with side effect
func SentryInit(conf *config.Config) error {
	if conf.SentryDSN == "" {
		log.Warn().Str("evt.name", "infra.sentry.init").Msg("Sentry is disabled due to missing DSN.")
		return nil
	} else {
		log.Info().Str("evt.name", "infra.sentry.init").Msg("Initializing Sentry...")
	}

	return sentry.Init(sentry.ClientOptions{
		Dsn:              conf.SentryDSN,
		Release:          "backend-next@" + bininfo.Version,
		Debug:            conf.DevMode,
		AttachStacktrace: true,
		TracesSampleRate: 0.01,
		Environment:      getEnvironment(conf),
		BeforeSend: func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
			if conf.DevMode {
				log.Trace().
					Str("evt.name", "infra.sentry.event").
					Interface("event", event).
					Interface("hint", hint).
					Msg("Sentry event captured, but not sent due to development mode enabled.")
				return nil
			}
			return event
		},
	})
}
