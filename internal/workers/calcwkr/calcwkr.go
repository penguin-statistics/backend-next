package calcwkr

import (
	"context"
	"net/http"
	"time"

	"github.com/avast/retry-go/v3"
	"github.com/go-redsync/redsync/v4"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/config"
	"exusiai.dev/backend-next/internal/service"
	"exusiai.dev/gommon/constant"
)

type WorkerDeps struct {
	fx.In
	DropMatrixService    *service.DropMatrix
	PatternMatrixService *service.PatternMatrix
	TrendService         *service.Trend
	SiteStatsService     *service.SiteStats
	RedSync              *redsync.Redsync
}

type Worker struct {
	// count counts batches worker has completed so far
	count int

	// sep describes the separation time in-between different jobs
	sep time.Duration

	// interval describes the interval in-between different batches of stats job running
	interval time.Duration

	// trendInterval describes the interval in-between different batches of trends job running
	trendInterval time.Duration

	// timeout describes the timeout for the worker
	timeout time.Duration

	// heartbeatURL allows the worker to ping a specified URL on succeed, to ensure worker is alive.
	// The key is the name of the worker, and the value is the URL.
	// Possible keys are: "stats", "trends"
	heartbeatURL map[string]string

	syncMutex *redsync.Mutex

	WorkerDeps
}

type WorkerCalcType string

var (
	WorkerCalcTypeStatsCalc  = WorkerCalcType("stats")
	WorkerCalcTypeTrendsCalc = WorkerCalcType("trends")
)

func (t WorkerCalcType) URL(w *Worker) string {
	return w.heartbeatURL[string(t)]
}

func (t WorkerCalcType) Interval(w *Worker) time.Duration {
	switch t {
	case WorkerCalcTypeStatsCalc:
		return w.interval
	case WorkerCalcTypeTrendsCalc:
		return w.trendInterval
	default:
		panic("unknown worker type")
	}
}

func Start(conf *config.Config, deps WorkerDeps) {
	if conf.WorkerEnabled {
		if len(conf.WorkerHeartbeatURL) == 0 {
			log.Warn().
				Str("evt.name", "worker.calcwkr.heartbeat.disabled").
				Msg("No heartbeat URL found. The worker will NOT send a heartbeat when it is finished.")
		} else {
			log.Info().
				Str("evt.name", "worker.calcwkr.heartbeat.enabled").
				Interface("heartbeatURLs", conf.WorkerHeartbeatURL).
				Msg("The worker will send a heartbeat to those URLs when it is finished")
		}
		w := &Worker{
			sep:           conf.WorkerSeparation,
			interval:      conf.WorkerInterval,
			trendInterval: conf.WorkerTrendInterval,
			timeout:       conf.WorkerTimeout,
			heartbeatURL:  conf.WorkerHeartbeatURL,
			syncMutex:     deps.RedSync.NewMutex("mutex:calcwkr", redsync.WithExpiry(30*time.Second), redsync.WithTries(2)),
			WorkerDeps:    deps,
		}
		w.checkConfig()
		w.doMainCalc(conf.MatrixWorkerSourceCategories)
		w.doTrendCalc(conf.MatrixWorkerSourceCategories)
	} else {
		log.Info().
			Str("evt.name", "worker.calcwkr.disabled").
			Msg("worker is disabled due to configuration")
	}
}

func (w *Worker) checkConfig() {
	if w.sep < 0 {
		panic("worker separation time cannot be negative")
	}
	if w.interval < 0 {
		panic("worker interval cannot be negative")
	}
	if w.trendInterval < 0 {
		panic("worker trend interval cannot be negative")
	}
	if w.timeout < 0 {
		panic("worker timeout cannot be negative")
	}
}

func (w *Worker) doMainCalc(sourceCategories []string) {
	w.task(context.Background(), WorkerCalcTypeStatsCalc, func(ctx context.Context, server string) error {
		var err error

		// DropMatrixService
		if err = w.microtask(ctx, WorkerCalcTypeStatsCalc, "dropMatrix", server, func() error {
			return w.DropMatrixService.RefreshAllDropMatrixElements(ctx, server, sourceCategories)
		}); err != nil {
			return err
		}
		time.Sleep(w.sep)

		// PatternMatrixService
		if err = w.microtask(ctx, WorkerCalcTypeStatsCalc, "patternMatrix", server, func() error {
			return w.PatternMatrixService.RefreshAllPatternMatrixElements(ctx, server, sourceCategories)
		}); err != nil {
			return err
		}
		time.Sleep(w.sep)

		// SiteStatsService
		if err = w.microtask(ctx, WorkerCalcTypeStatsCalc, "siteStats", server, func() error {
			_, err := w.SiteStatsService.RefreshShimSiteStats(ctx, server)
			return err
		}); err != nil {
			return err
		}

		return nil
	})
}

func (w *Worker) doTrendCalc(sourceCategories []string) {
	w.task(context.Background(), WorkerCalcTypeTrendsCalc, func(ctx context.Context, server string) error {
		var err error

		// TrendService
		if err = w.microtask(ctx, WorkerCalcTypeTrendsCalc, "trend", server, func() error {
			return w.TrendService.RefreshTrendElements(ctx, server, sourceCategories)
		}); err != nil {
			return err
		}

		return nil
	})
}

func (w *Worker) lock() error {
	return w.syncMutex.Lock()
}

func (w *Worker) unlock() {
	b, err := w.syncMutex.Unlock()
	if err != nil {
		log.Error().Str("evt.name", "worker").Err(err).Msg("unlock sync mutex failed")
	}
	if !b {
		log.Error().Str("evt.name", "worker").Msg("unlock sync mutex failed: not locked. this should not happen as it indicates the mutex is unlocked before the worker finished.")
	}
}

func (w *Worker) extend() {
	// extends the sync mutex to ensure lock is held for enough duration
	ok, err := w.syncMutex.Extend()
	if err != nil {
		log.Error().Str("evt.name", "worker.calcwkr").Err(err).Msg("failed to extend sync mutex")
		return
	}
	if !ok {
		log.Error().Str("evt.name", "worker.calcwkr").Msg("failed to extend sync mutex: not locked. this should not happen as it indicates the mutex is unlocked before the worker finished.")
		return
	}
}

func (w *Worker) task(ctx context.Context, typ WorkerCalcType, f func(ctx context.Context, server string) error) {
	logger := log.With().Str("evt.name", "worker.calcwkr."+string(typ)).Logger()
	parentCtx := logger.WithContext(ctx)

	go func() {
		time.Sleep(time.Second * 3)

		for {
			log.Ctx(parentCtx).Info().Int("count", w.count).Msg("worker batch timer fired. acquiring limiter lock...")
			if err := w.lock(); err != nil {
				log.Ctx(parentCtx).Warn().Err(err).Msg("failed to acquire lock, perhaps the resource is currently busy. sleeping for 30 seconds and trying again...")
				time.Sleep(time.Second * 30)
				continue
			}
			log.Ctx(parentCtx).Info().Int("count", w.count).Msg("successfully acquired limiter lock")

			ctx, cancel := context.WithTimeout(parentCtx, w.timeout)

			func() {
				defer func() {
					w.count++
					cancel()
					w.unlock()
					time.Sleep(typ.Interval(w))
				}()

				errChan := make(chan error)
				go func() {
					for _, server := range constant.Servers {
						err := f(ctx, server)
						if err != nil {
							errChan <- err
							return
						}
					}
					errChan <- nil
				}()

				select {
				case <-ctx.Done():
					log.Ctx(ctx).Error().Int("count", w.count).Err(ctx.Err()).Msg("worker timeout reached")
					return
				case err := <-errChan:
					if err != nil {
						log.Ctx(ctx).Error().Int("count", w.count).Err(err).Msg("worker unexpected error occurred while running batch")
						return
					}
				}

				log.Ctx(ctx).Info().Int("count", w.count).Msg("worker batch finished")

				go func() {
					w.heartbeat(typ)
				}()
			}()
		}
	}()
}

func (w *Worker) microtask(ctx context.Context, typ WorkerCalcType, service, server string, f func() error) error {
	mutexNotifierTicker := time.NewTicker(time.Second * 10)
	defer func() {
		mutexNotifierTicker.Stop()
	}()

	go func() {
		w.extend()
		for {
			select {
			case <-mutexNotifierTicker.C:
				w.extend()
			case <-ctx.Done():
				return
			}
		}
	}()

	log.Ctx(ctx).Info().Str("evt.name", "worker.calcwkr."+string(typ)+"."+service).Str("server", server).Msg("worker microtask started calculating")
	if err := observeCalcDuration(service, server, f); err != nil {
		log.Ctx(ctx).Error().Str("evt.name", "worker.calcwkr."+string(typ)+"."+service).Str("server", server).Err(err).Msg("worker microtask failed")
		return err
	}
	log.Ctx(ctx).Info().Str("evt.name", "worker.calcwkr."+string(typ)+"."+service).Str("server", server).Msg("worker microtask finished")

	return nil
}

func (w *Worker) heartbeat(typ WorkerCalcType) {
	url := typ.URL(w)
	if url == "" {
		// we simply ignore if there's no heartbeat URL
		return
	}

	c := &http.Client{
		Timeout: time.Second * 5,
	}
	err := retry.Do(func() error {
		r, err := c.Get(url)
		if err != nil {
			return err
		}
		if r.StatusCode < 200 || r.StatusCode >= 300 {
			return errors.Errorf("worker succeeded notification: invalid status code: %d", r.StatusCode)
		}
		return nil
	}, retry.Attempts(5))

	if err != nil {
		log.Error().
			Err(err).
			Str("evt.name", "worker.calcwkr.heartbeat.failed").
			Str("url", url).
			Msg("worker succeeded notification eventually failed after retry attempts")
	} else {
		log.Info().
			Str("evt.name", "worker.calcwkr.heartbeat.success").
			Str("url", url).
			Msg("worker succeeded notification succeeded")
	}
}

func (w *Worker) Count() int {
	return w.count
}
