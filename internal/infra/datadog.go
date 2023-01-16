package infra

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"go.uber.org/fx"
	"gopkg.in/DataDog/dd-trace-go.v1/profiler"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/pkg/bininfo"
)

func Datadog(conf *appconfig.Config, lc fx.Lifecycle) {
	if conf.DevMode {
		log.Info().
			Str("evt.name", "infra.datadog.disabled").
			Msg("datadog profiler is disabled in dev mode")
		return
	}

	if !conf.DatadogProfilerEnabled {
		log.Info().
			Str("evt.name", "infra.datadog.disabled").
			Msg("datadog profiler is disabled")
		return
	}

	lc.Append(
		fx.Hook{
			OnStart: func(ctx context.Context) error {
				err := profiler.Start(
					profiler.WithService("pgbackend"),
					profiler.WithEnv(lo.Ternary(conf.DevMode, "dev", "prod")),
					profiler.WithVersion(bininfo.Version),
					profiler.WithAgentAddr(conf.DatadogProfilerAgentAddress),
					profiler.WithProfileTypes(
						profiler.CPUProfile,
						profiler.HeapProfile,
						// The profiles below are disabled by default to keep overhead
						// low, but can be enabled as needed.

						// profiler.BlockProfile,
						// profiler.MutexProfile,
						// profiler.GoroutineProfile,
					),
				)
				if err != nil {
					log.Error().
						Err(err).
						Str("evt.name", "infra.datadog.error").
						Msg("datadog profiler failed to start")
				}

				// datadog profiler is not a critical component, so we don't return error here
				return nil
			},
			OnStop: func(ctx context.Context) error {
				profiler.Stop()
				return nil
			},
		},
	)
}
