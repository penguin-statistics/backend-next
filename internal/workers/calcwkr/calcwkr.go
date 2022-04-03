package calcwkr

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/constant"
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

	// timeout describes the timeout for the worker
	timeout time.Duration

	WorkerDeps
}

func Start(conf *config.Config, deps WorkerDeps) {
	if conf.WorkerEnabled {
		(&Worker{
			sep:        conf.WorkerSeparation,
			interval:   conf.WorkerInterval,
			timeout:    conf.WorkerTimeout,
			WorkerDeps: deps,
		}).do()
	} else {
		log.Info().Msg("worker is disabled due to configuration")
	}
}

func (w *Worker) do() {
	ctx := context.Background()

	go func() {
		for {
			log.Info().
				Int("count", w.count).
				Msg("worker batch started")

			func() {
				sessCtx, sessCancel := context.WithTimeout(ctx, w.timeout)
				defer func() {
					w.count++
					time.Sleep(w.interval)
					sessCancel()
				}()

				errChan := make(chan error)
				go func() {
					for _, server := range constant.Servers {
						log.Info().Str("server", server).Str("service", "DropMatrixService").Msg("worker microtask started calculating")
						if err := w.DropMatrixService.RefreshAllDropMatrixElements(sessCtx, server); err != nil {
							log.Error().Err(err).Str("server", server).Str("service", "DropMatrixService").Msg("worker microtask failed")
							errChan <- err
							return
						}
						log.Info().Str("server", server).Str("service", "DropMatrixService").Msg("worker microtask finished")
						time.Sleep(w.sep)

						log.Info().Str("server", server).Str("service", "PatternMatrixService").Msg("worker microtask started calculating")
						if err := w.PatternMatrixService.RefreshAllPatternMatrixElements(sessCtx, server); err != nil {
							log.Error().Err(err).Str("server", server).Str("service", "PatternMatrixService").Msg("worker microtask failed")
							errChan <- err
							return
						}
						log.Info().Str("server", server).Str("service", "PatternMatrixService").Msg("worker microtask finished")
						time.Sleep(w.sep)

						log.Info().Str("server", server).Str("service", "TrendService").Msg("worker microtask started calculating")
						if err := w.TrendService.RefreshTrendElements(sessCtx, server); err != nil {
							log.Error().Err(err).Str("server", server).Str("service", "TrendService").Msg("worker microtask failed")
							errChan <- err
							return
						}
						log.Info().Str("server", server).Str("service", "TrendService").Msg("worker microtask finished")
						time.Sleep(w.sep)

						log.Info().Str("server", server).Str("service", "SiteStatsService").Msg("worker microtask started calculating")
						if _, err := w.SiteStatsService.RefreshShimSiteStats(sessCtx, server); err != nil {
							log.Error().Err(err).Str("server", server).Str("service", "SiteStatsService").Msg("worker microtask failed")
							errChan <- err
							return
						}
						log.Info().Str("server", server).Str("service", "SiteStatsService").Msg("worker microtask finished")
						time.Sleep(w.sep)
					}
					errChan <- nil
				}()

				select {
				case <-sessCtx.Done():
					log.Error().Err(sessCtx.Err()).Int("count", w.count).Msg("worker timeout reached")
					return
				case err := <-errChan:
					if err != nil {
						log.Error().Err(err).Int("count", w.count).Msg("worker unexpected error occurred while running batch")
						return
					}
				}

				log.Info().Int("count", w.count).Msg("worker batch finished")
			}()
		}
	}()
}

func (w *Worker) Count() int {
	return w.count
}
