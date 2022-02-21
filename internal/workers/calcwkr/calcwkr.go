package calcwkr

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type WorkerDeps struct {
	fx.In
	DropMatrixService    *service.DropMatrixService
	PatternMatrixService *service.PatternMatrixService
	TrendService         *service.TrendService
	SiteStatsService     *service.SiteStatsService
}

type Worker struct {
	// count counts batches worker has completed so far
	count int

	// sep describes the separation time in-between different jobs
	sep time.Duration

	// interval describes the interval in-between different batches of job running
	interval time.Duration

	// deps
	WorkerDeps
}

func Start(config *config.Config, deps WorkerDeps) {
	(&Worker{
		sep:        config.WorkerSeparation,
		interval:   config.WorkerInterval,
		WorkerDeps: deps,
	}).do()
}

func (w *Worker) do() context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			log.Info().
				Int("count", w.count).
				Msg("worker batch started")

			for _, server := range constants.Servers {
				log.Info().Str("server", server).Str("service", "DropMatrixService").Msg("worker calculating")
				if err := w.DropMatrixService.RefreshAllDropMatrixElements(ctx, server); err != nil {
					continue
				}
				log.Debug().Str("server", server).Str("service", "PatternMatrixService").Msg("worker finished")
				time.Sleep(w.sep)

				log.Info().Str("server", server).Str("service", "PatternMatrixService").Msg("worker calculating")
				if err := w.PatternMatrixService.RefreshAllPatternMatrixElements(ctx, server); err != nil {
					continue
				}
				log.Debug().Str("server", server).Str("service", "PatternMatrixService").Msg("worker finished")
				time.Sleep(w.sep)

				log.Info().Str("server", server).Str("service", "TrendService").Msg("worker calculating")
				if err := w.TrendService.RefreshTrendElements(ctx, server); err != nil {
					continue
				}
				log.Debug().Str("server", server).Str("service", "TrendService").Msg("worker finished")
				time.Sleep(w.sep)

				log.Info().Str("server", server).Str("service", "SiteStatsService").Msg("worker calculating")
				if _, err := w.SiteStatsService.RefreshShimSiteStats(ctx, server); err != nil {
					continue
				}
				log.Debug().Str("server", server).Str("service", "SiteStatsService").Msg("worker finished")
				time.Sleep(w.sep)
			}

			log.Info().Int("count", w.count).Msg("worker batch finished")

			w.count++
			time.Sleep(w.interval)
		}
	}()

	return cancel
}

func (w *Worker) Count() int {
	return w.count
}
