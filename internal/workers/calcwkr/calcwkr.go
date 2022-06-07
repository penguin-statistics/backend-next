package calcwkr

import (
	"context"
	"net/http"
	"time"

	"github.com/avast/retry-go/v3"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
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
	logger := log.With().Str("service", "worker:calculator").Logger()
	parentCtx := logger.WithContext(context.Background())

	go func() {
		time.Sleep(time.Second * 3)

		for {
			ctx, cancel := context.WithTimeout(parentCtx, w.timeout)
			log.Ctx(ctx).UpdateContext(func(c zerolog.Context) zerolog.Context {
				return c.Int("count", w.count)
			})
			log.Ctx(ctx).Info().Msg("worker batch started")

			func() {
				defer func() {
					w.count++
					time.Sleep(w.interval)
					cancel()
				}()

				errChan := make(chan error)
				go func() {
					for _, server := range constant.Servers {
						// DropMatrixService
						log.Ctx(ctx).UpdateContext(func(c zerolog.Context) zerolog.Context {
							return c.Str("server", server).Str("service", "worker:calculator:dropMatrix")
						})

						log.Ctx(ctx).Info().Msg("worker microtask started calculating")
						if err := w.DropMatrixService.RefreshAllDropMatrixElements(ctx, server, sourceCategories); err != nil {
							log.Ctx(ctx).Error().Err(err).Msg("worker microtask failed")
							errChan <- err
							return
						}
						log.Ctx(ctx).Info().Msg("worker microtask finished")
						time.Sleep(w.sep)

						// PatternMatrixService
						log.Ctx(ctx).UpdateContext(func(c zerolog.Context) zerolog.Context {
							return c.Str("service", "worker:calculator:patternMatrix")
						})
						log.Ctx(ctx).Info().Msg("worker microtask started calculating")
						if err := w.PatternMatrixService.RefreshAllPatternMatrixElements(ctx, server, sourceCategories); err != nil {
							log.Ctx(ctx).Error().Err(err).Msg("worker microtask failed")
							errChan <- err
							return
						}
						log.Ctx(ctx).Info().Msg("worker microtask finished")
						time.Sleep(w.sep)

						// TrendService
						log.Ctx(ctx).UpdateContext(func(c zerolog.Context) zerolog.Context {
							return c.Str("service", "worker:calculator:trend")
						})
						log.Ctx(ctx).Info().Msg("worker microtask started calculating")
						if err := w.TrendService.RefreshTrendElements(ctx, server, sourceCategories); err != nil {
							log.Ctx(ctx).Error().Err(err).Msg("worker microtask failed")
							errChan <- err
							return
						}
						log.Ctx(ctx).Info().Msg("worker microtask finished")
						time.Sleep(w.sep)

						// SiteStatsService
						log.Ctx(ctx).UpdateContext(func(c zerolog.Context) zerolog.Context {
							return c.Str("service", "worker:calculator:siteStats")
						})
						log.Ctx(ctx).Info().Msg("worker microtask started calculating")
						if _, err := w.SiteStatsService.RefreshShimSiteStats(ctx, server); err != nil {
							log.Ctx(ctx).Error().Err(err).Msg("worker microtask failed")
							errChan <- err
							return
						}
						log.Ctx(ctx).Info().Msg("worker microtask finished")
						time.Sleep(w.sep)
					}
					errChan <- nil
				}()

				select {
				case <-ctx.Done():
					log.Ctx(ctx).Error().Err(ctx.Err()).Msg("worker timeout reached")
					return
				case err := <-errChan:
					if err != nil {
						log.Ctx(ctx).Error().Err(err).Msg("worker unexpected error occurred while running batch")
						return
					}
				}

				log.Ctx(ctx).UpdateContext(func(c zerolog.Context) zerolog.Context {
					return c.Str("service", "worker:calculator")
				})

				log.Ctx(ctx).Info().Msg("worker batch finished")

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
