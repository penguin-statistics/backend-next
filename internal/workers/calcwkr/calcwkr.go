package calcwkr

import (
	"context"
	"net/http"
	"time"

	"github.com/avast/retry-go"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type WorkerDeps struct {
	fx.In
	DropMatrixService    *service.DropMatrix
	PatternMatrixService *service.PatternMatrix
	TrendService         *service.Trend
	SiteStatsService     *service.SiteStats
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

	// heartbeatURL allows the worker to ping a specified URL on succeed, to ensure worker is alive
	heartbeatURL string

	WorkerDeps
}

func Start(conf *config.Config, deps WorkerDeps) {
	if conf.WorkerEnabled {
		if conf.WorkerHeartbeatURL == "" {
			log.Info().
				Msg("No heartbeat URL found. The worker will NOT send a heartbeat when it is finished.")
		}
		(&Worker{
			sep:          conf.WorkerSeparation,
			interval:     conf.WorkerInterval,
			timeout:      conf.WorkerTimeout,
			heartbeatURL: conf.WorkerHeartbeatURL,
			WorkerDeps:   deps,
		}).do(conf.MatrixWorkerSourceCategories)
	} else {
		log.Info().Msg("worker is disabled due to configuration")
	}
}

func (w *Worker) do(sourceCategories []string) {
	ctx := context.Background()

	go func() {
		time.Sleep(time.Second * 3)

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
						log.Info().Str("server", server).Str("component", "DropMatrixService").Msg("worker microtask started calculating")
						if err := w.DropMatrixService.RefreshAllDropMatrixElements(sessCtx, server, sourceCategories); err != nil {
							log.Error().Err(err).Str("server", server).Str("component", "DropMatrixService").Msg("worker microtask failed")
							errChan <- err
							return
						}
						log.Info().Str("server", server).Str("component", "DropMatrixService").Msg("worker microtask finished")
						time.Sleep(w.sep)

						log.Info().Str("server", server).Str("component", "PatternMatrixService").Msg("worker microtask started calculating")
						if err := w.PatternMatrixService.RefreshAllPatternMatrixElements(sessCtx, server, sourceCategories); err != nil {
							log.Error().Err(err).Str("server", server).Str("component", "PatternMatrixService").Msg("worker microtask failed")
							errChan <- err
							return
						}
						log.Info().Str("server", server).Str("component", "PatternMatrixService").Msg("worker microtask finished")
						time.Sleep(w.sep)

						log.Info().Str("server", server).Str("component", "TrendService").Msg("worker microtask started calculating")
						if err := w.TrendService.RefreshTrendElements(sessCtx, server, sourceCategories); err != nil {
							log.Error().Err(err).Str("server", server).Str("component", "TrendService").Msg("worker microtask failed")
							errChan <- err
							return
						}
						log.Info().Str("server", server).Str("component", "TrendService").Msg("worker microtask finished")
						time.Sleep(w.sep)

						log.Info().Str("server", server).Str("component", "SiteStatsService").Msg("worker microtask started calculating")
						if _, err := w.SiteStatsService.RefreshShimSiteStats(sessCtx, server); err != nil {
							log.Error().Err(err).Str("server", server).Str("component", "SiteStatsService").Msg("worker microtask failed")
							errChan <- err
							return
						}
						log.Info().Str("server", server).Str("component", "SiteStatsService").Msg("worker microtask finished")
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

				go func() {
					w.heartbeat()
				}()
			}()
		}
	}()
}

func (w *Worker) heartbeat() {
	if w.heartbeatURL == "" {
		// we simply ignore if there's no heartbeat URL
		return
	}

	c := (&http.Client{
		Timeout: time.Second * 5,
	})
	err := retry.Do(func() error {
		r, err := c.Get(w.heartbeatURL)
		if err != nil {
			return err
		}
		if r.StatusCode < 200 || r.StatusCode >= 300 {
			return errors.Errorf("succeeded notification: invalid status code: %d", r.StatusCode)
		}
		return nil
	}, retry.Attempts(5))
	if err != nil {
		log.Error().
			Err(err).
			Msg("worker succeeded notification eventually failed")
	}
}

func (w *Worker) Count() int {
	return w.count
}
