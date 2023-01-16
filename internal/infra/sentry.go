package infra

import (
	"github.com/getsentry/sentry-go"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/pkg/bininfo"
)

// SentryInit initializes sentry with side effect
func SentryInit(conf *appconfig.Config) error {
	if conf.SentryDSN == "" {
		log.Warn().
			Str("evt.name", "infra.sentry.init").
			Msg("Sentry is disabled due to missing DSN.")
		return nil
	} else {
		log.Info().
			Str("evt.name", "infra.sentry.init").
			Msg("Initializing Sentry...")
	}

	return sentry.Init(sentry.ClientOptions{
		Dsn:              conf.SentryDSN,
		Release:          bininfo.Version,
		Debug:            conf.DevMode,
		AttachStacktrace: true,
		TracesSampleRate: 0.01,
		Environment:      lo.Ternary(conf.DevMode, "dev", "prod"),
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
