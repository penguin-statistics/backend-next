package calcwkr

import (
	"context"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/avast/retry-go/v4"
	"github.com/go-redsync/redsync/v4"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/fx"
	"golang.org/x/sync/errgroup"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/service"
)

// tracer traces every batch: calcwkr.batch > calcwkr.server > calcwkr.<microtask>, with the database queries of a
// microtask below it. Spans named calcwkr.* are always sampled (see httpserver).
var tracer = otel.Tracer("calcwkr")

type WorkerDeps struct {
	fx.In

	Config               *appconfig.Config
	DropMatrixService    *service.DropMatrix
	PatternMatrixService *service.PatternMatrix
	TrendService         *service.Trend
	SiteStatsService     *service.SiteStats
	ArchiveService       *service.Archive
	RedSync              *redsync.Redsync
}

type Worker struct {
	// count counts batches worker has completed so far
	count int

	// sep describes the separation time in-between different jobs
	sep time.Duration

	// interval describes the interval in-between different batches of stats job running
	interval time.Duration

	// timeout describes the timeout for the worker
	timeout time.Duration

	// heartbeatURL allows the worker to ping a specified URL on succeed, to ensure worker is alive.
	// The key is the name of the worker, and the value is the URL.
	// Possible keys are: "stats", "trends"
	heartbeatURL map[string]string

	syncMutex *redsync.Mutex
	// syncMutexMu serializes operations on syncMutex, which is not safe for concurrent use: the microtasks of
	// servers calculated concurrently renew it at the same time
	syncMutexMu sync.Mutex

	// refreshGlobalMatrices tells whether a batch refreshes the cached global matrices (see globalMatrixRefresh)
	refreshGlobalMatrices bool
	// refreshMu runs the global matrix refreshes one at a time, as requests calculated them before, to not load the
	// database with those of every server at once while the batch still calculates others
	refreshMu sync.Mutex

	WorkerDeps
}

type WorkerCalcType string

var WorkerCalcTypeStatsCalc = WorkerCalcType("stats")

func (t WorkerCalcType) URL(w *Worker) string {
	return w.heartbeatURL[string(t)]
}

func Start(conf *appconfig.Config, deps WorkerDeps) {
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
			sep:          conf.WorkerSeparation,
			interval:     conf.WorkerInterval,
			timeout:      conf.WorkerTimeout,
			heartbeatURL: conf.WorkerHeartbeatURL,
			// the worker of a process serving requests
			refreshGlobalMatrices: true,
			syncMutex:             deps.RedSync.NewMutex("mutex:calcwkr", redsync.WithExpiry(30*time.Second), redsync.WithTries(2)),
			WorkerDeps:            deps,
		}
		w.checkConfig()
		w.doMainCalc(conf.MatrixWorkerSourceCategories)
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
	if w.timeout < 0 {
		panic("worker timeout cannot be negative")
	}
}

func (w *Worker) doMainCalc(sourceCategories []string) {
	w.task(context.Background(), WorkerCalcTypeStatsCalc, w.calcStats)
}

// globalMatrixRefresh replaces the cached global matrices (or trend) of server with ones calculated from the matrix
// elements a job just saved, by refresh, right after the job: the jobs no longer drop them, so a later failure of the
// batch must not keep them stale. It runs in the background, outside of the batch, which holds the worker lock, and of
// its timeout; requests get the previous matrices until then.
func (w *Worker) globalMatrixRefresh(batchCtx context.Context, name, server string, refresh func(ctx context.Context, server string) error) {
	if !w.refreshGlobalMatrices {
		return
	}
	logger := log.Ctx(batchCtx).With().Str("evt.name", "worker.calcwkr."+name).Str("server", server).Logger()
	go func() {
		w.refreshMu.Lock()
		defer w.refreshMu.Unlock()

		ctx, cancel := context.WithTimeout(logger.WithContext(context.Background()), w.timeout)
		defer cancel()
		ctx, span := tracer.Start(ctx, "calcwkr."+name, trace.WithAttributes(attribute.String("server", server)))
		var panicked bool
		dur, err := observeCalcDuration(name, server, func() (err error) {
			// requests have the recovery of the http server, this goroutine has none: a panic would end the process
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					err = errors.Errorf("panic: %v\n%s", r, debug.Stack())
				}
			}()
			return refresh(ctx, server)
		})
		endSpan(span, err)
		outcome := "ok"
		if panicked {
			outcome = "panic"
		} else if err != nil {
			outcome = "error"
		}
		observability.WorkerRefreshDuration.WithLabelValues(name, server).Observe(dur.Seconds())
		observability.WorkerRefreshes.WithLabelValues(name, server, outcome).Inc()
		if err != nil {
			logger.Error().Err(err).Msg("failed to refresh the cached global results: dropped them, requests recalculate them")
			return
		}
		logger.Info().Msg("refreshed the cached global results")
	}()
}

// warmUpGlobalCaches fills the caches of the global matrices and trends of every server, empty after the process
// started, with fill (fillGlobalCaches), from the matrix elements earlier batches saved: requests then do not each
// wait for a calculation until the first batch has refreshed them. fill goes through the caches as requests do, so
// it joins a calculation a request already runs and skips what requests already cached, instead of calculating it
// once more. The first batch runs it before its jobs, holding the worker lock, so that it does not read the matrix
// elements while another process's batch replaces them, and holding refreshMu, one server at a time. A server that
// fails is left to the requests, as before.
func (w *Worker) warmUpGlobalCaches(ctx context.Context, fill func(ctx context.Context, server string) error) {
	if !w.refreshGlobalMatrices {
		return
	}
	w.refreshMu.Lock()
	defer w.refreshMu.Unlock()
	for _, server := range constant.Servers {
		server := server
		// microtask keeps the worker lock and logs the error
		_ = w.microtask(ctx, "globalCachesWarmUp", server, func(ctx context.Context) (err error) {
			// a panic would end the process on every start: fail the server instead
			defer func() {
				if r := recover(); r != nil {
					err = errors.Errorf("panic: %v\n%s", r, debug.Stack())
				}
			}()
			return fill(ctx, server)
		})
	}
}

// cacheWarmUp is a warm-up of cached results of a server, named in the logs.
type cacheWarmUp struct {
	name   string
	warmUp func(ctx context.Context, server string) error
}

// fillGlobalCaches fills the cached global matrices and trend of server through the cached getters, as requests do:
// those already cached are skipped, and a calculation a request has in flight is waited for instead of run again.
func (w *Worker) fillGlobalCaches(ctx context.Context, server string) error {
	return warmUpEach(ctx, server, []cacheWarmUp{
		{"globalDropMatrix", w.DropMatrixService.WarmUpGlobalDropMatrixCaches},
		{"globalPatternMatrix", w.PatternMatrixService.WarmUpGlobalPatternMatrixCaches},
		{"shimTrend", w.TrendService.WarmUpShimTrend},
	})
}

// warmUpEach runs every warm-up of server, also after one failed. It logs every failure and returns an error naming
// the failed warm-ups.
func warmUpEach(ctx context.Context, server string, warmUps []cacheWarmUp) error {
	var failed []string
	for _, wu := range warmUps {
		if err := wu.warmUp(ctx, server); err != nil {
			log.Ctx(ctx).Error().Str("evt.name", "worker.calcwkr.globalCachesWarmUp").Str("server", server).Str("cache", wu.name).Err(err).
				Msg("failed to warm up the cached global results: requests calculate them")
			failed = append(failed, wu.name)
		}
	}
	if len(failed) > 0 {
		return errors.Errorf("failed to warm up %s", strings.Join(failed, ", "))
	}
	return nil
}

// calcStats runs every stats calculation of a single server.
func (w *Worker) calcStats(ctx context.Context, server string) (err error) {
	ctx, span := tracer.Start(ctx, "calcwkr.server", trace.WithAttributes(attribute.String("server", server)))
	defer func() { endSpan(span, err) }()

	// DropMatrixService
	if err = w.microtask(ctx, "dropMatrix", server, func(ctx context.Context) error {
		return w.DropMatrixService.RunCalcDropMatrixJob(ctx, server)
	}); err != nil {
		return err
	}
	w.globalMatrixRefresh(ctx, "globalDropMatrixRefresh", server, w.DropMatrixService.RefreshGlobalDropMatrixCaches)
	// the trend is calculated from the drop matrix elements the job saved
	w.globalMatrixRefresh(ctx, "shimTrendRefresh", server, w.TrendService.RefreshShimTrend)
	time.Sleep(w.sep)

	// PatternMatrixService
	if err = w.microtask(ctx, "patternMatrix", server, func(ctx context.Context) error {
		return w.PatternMatrixService.RunCalcPatternMatrixJob(ctx, server)
	}); err != nil {
		return err
	}
	w.globalMatrixRefresh(ctx, "globalPatternMatrixRefresh", server, w.PatternMatrixService.RefreshGlobalPatternMatrixCaches)
	time.Sleep(w.sep)

	// SiteStatsService
	if err = w.microtask(ctx, "siteStats", server, func(ctx context.Context) error {
		_, err := w.SiteStatsService.RefreshShimSiteStats(ctx, server)
		return err
	}); err != nil {
		return err
	}

	// server == "CN": we only run archive job on a singular server
	if w.Config.DropReportArchiveEnabled && server == "CN" {
		// Archive
		if err = w.microtask(ctx, "archive", server, func(ctx context.Context) error {
			err := w.ArchiveService.ArchiveByGlobalConfig(ctx)
			return err
		}); err != nil {
			return err
		}
	}

	return nil
}

func (w *Worker) lock() error {
	w.syncMutexMu.Lock()
	defer w.syncMutexMu.Unlock()
	return w.syncMutex.Lock()
}

func (w *Worker) unlock() {
	w.syncMutexMu.Lock()
	b, err := w.syncMutex.Unlock()
	w.syncMutexMu.Unlock()
	if err != nil {
		log.Error().Str("evt.name", "worker").Err(err).Msg("unlock sync mutex failed")
	}
	if !b {
		log.Error().Str("evt.name", "worker").Msg("unlock sync mutex failed: not locked. this should not happen as it indicates the mutex is unlocked before the worker finished.")
	}
}

func (w *Worker) extend() {
	// extends the sync mutex to ensure lock is held for enough duration
	w.syncMutexMu.Lock()
	ok, err := w.syncMutex.Extend()
	w.syncMutexMu.Unlock()
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
			w.batch(parentCtx, typ, f)
		}
	}()
}

// lockRetryDelay is how long a worker that did not acquire the lock waits before trying again.
var lockRetryDelay = 30 * time.Second

// batch runs f for every server once, holding the worker lock, then waits for the interval; or, when another worker
// holds the lock, waits for lockRetryDelay.
func (w *Worker) batch(parentCtx context.Context, typ WorkerCalcType, f func(ctx context.Context, server string) error) {
	log.Ctx(parentCtx).Info().Int("count", w.count).Msg("worker batch timer fired. acquiring limiter lock...")
	if err := w.lock(); err != nil {
		observability.WorkerCalcBatches.WithLabelValues("lock_not_acquired").Inc()
		log.Ctx(parentCtx).Warn().Err(err).Msg("failed to acquire lock, perhaps the resource is currently busy. sleeping for 30 seconds and trying again...")
		time.Sleep(lockRetryDelay)
		return
	}
	log.Ctx(parentCtx).Info().Int("count", w.count).Msg("successfully acquired limiter lock")

	ctx, cancel := context.WithTimeout(parentCtx, w.timeout)
	ctx, span := tracer.Start(ctx, "calcwkr.batch", trace.WithAttributes(
		attribute.String("type", string(typ)), attribute.Int("count", w.count)))

	start := time.Now()
	outcome := "ok"
	var batchErr error
	defer func() {
		observability.WorkerCalcBatchDuration.Observe(time.Since(start).Seconds())
		observability.WorkerCalcBatches.WithLabelValues(outcome).Inc()
		endSpan(span, batchErr)
		w.count++
		cancel()
		w.unlock()
		time.Sleep(w.interval)
	}()

	// buffered: after a timeout nobody receives, and the goroutine must not leak
	errChan := make(chan error, 1)
	first := w.count == 0
	go func() {
		if first {
			w.warmUpGlobalCaches(ctx, w.fillGlobalCaches)
		}
		errChan <- runForAllServers(ctx, f)
	}()

	select {
	case <-ctx.Done():
		outcome, batchErr = "timeout", ctx.Err()
		log.Ctx(ctx).Error().Int("count", w.count).Err(ctx.Err()).Msg("worker timeout reached")
		return
	case err := <-errChan:
		if err != nil {
			outcome, batchErr = "error", err
			// a calculation that honours the cancellation returns ctx.Err() when the deadline passes, and this case
			// can win the select over ctx.Done(): count it as the timeout it is
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				outcome = "timeout"
			}
			log.Ctx(ctx).Error().Int("count", w.count).Err(err).Msg("worker unexpected error occurred while running batch")
			return
		}
	}

	log.Ctx(ctx).Info().Int("count", w.count).Msg("worker batch finished")

	go func() {
		w.heartbeat(typ)
	}()
}

// serverConcurrency is how many servers a batch calculates at the same time.
var serverConcurrency = 2

// runForAllServers runs f for every server concurrently. Servers write disjoint rows and cache keys, so they do
// not depend on each other. After an error, servers not started yet are skipped and the first error is returned;
// servers already running are not interrupted, so none is left halfway through replacing its matrix elements.
func runForAllServers(ctx context.Context, f func(ctx context.Context, server string) error) error {
	var (
		eg     errgroup.Group
		failed atomic.Bool
	)
	eg.SetLimit(serverConcurrency)
	for _, server := range constant.Servers {
		server := server
		eg.Go(func() error {
			if failed.Load() {
				return nil
			}
			if err := f(ctx, server); err != nil {
				failed.Store(true)
				return err
			}
			return nil
		})
	}
	return eg.Wait()
}

func (w *Worker) microtask(ctx context.Context, service, server string, f func(ctx context.Context) error) (err error) {
	ctx, span := tracer.Start(ctx, "calcwkr."+service, trace.WithAttributes(attribute.String("server", server)))
	defer func() { endSpan(span, err) }()

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

	log.Ctx(ctx).Info().Str("evt.name", "worker.calcwkr."+service).Str("server", server).Msg("worker microtask started calculating")
	dur, err := observeCalcDuration(service, server, func() error { return f(ctx) })
	observability.WorkerCalcPhaseDuration.WithLabelValues(service, server).Observe(dur.Seconds())
	if err != nil {
		log.Ctx(ctx).Error().Str("evt.name", "worker.calcwkr."+service).Str("server", server).Err(err).Msg("worker microtask failed")
		return err
	}
	log.Ctx(ctx).Info().Str("evt.name", "worker.calcwkr."+service).Str("server", server).Msg("worker microtask finished")

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

// endSpan ends span with the status of the work it covered.
func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
}
