// Package cachesync keeps the cached global results (the global drop and pattern matrices, the trend and the site
// stats) of every process in step with the refreshes of the calc worker. calcwkr refreshes them after its jobs in its
// own process only, while the processes serving requests may run no worker (PENGUIN_V3_WORKER_ENABLED=false, with the
// worker in a process of its own) or not hold the worker lock: their results stayed as first calculated until they
// expired (24h).
//
// After each refresh, the worker advances the generation of that kind of results of that server in Redis (Bump).
// Every process reads all generations every CacheSyncInterval and, for each that changed, refreshes those results in
// the background if it serves any of them: if a request is calculating one, or read one since it was cached (see
// cache.Set.Used). A process that does not (e.g. the worker's own, which serves no requests, and caches only what its
// worker calculated) deletes those it caches instead: a request, if any comes, calculates them from the new data, and
// the database is not loaded with results nobody reads. The refreshes run one at a time, with the worker's of the
// process too (RefreshLock), and one that is waiting already is not queued again.
//
// The generations are polled instead of published (pub/sub): a poll after Redis was unreachable or failed over still
// sees every change, where a message published meanwhile would be lost. A generation advances to the time of the bump
// (in milliseconds) if that is more than one more: one that Redis lost (flushed, or failed over to a replica that
// missed it) is not reached again by the next bumps, which a poll would take for no change.
package cachesync

import (
	"context"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/service"
)

// tracer traces every refresh: cachesync.<kind>, with the database queries of the refresh below it.
var tracer = otel.Tracer("cachesync")

// Kind is a kind of cached global results the worker refreshes, each refreshed by a single function for a server.
type Kind string

const (
	// KindGlobalDropMatrix is service.DropMatrix.RefreshGlobalDropMatrixCaches
	KindGlobalDropMatrix Kind = "globalDropMatrix"
	// KindGlobalPatternMatrix is service.PatternMatrix.RefreshGlobalPatternMatrixCaches
	KindGlobalPatternMatrix Kind = "globalPatternMatrix"
	// KindShimTrend is service.Trend.RefreshShimTrend
	KindShimTrend Kind = "shimTrend"
	// KindSiteStats is service.SiteStats.RefreshShimSiteStats
	KindSiteStats Kind = "siteStats"
)

// Kinds lists every kind, in the order the refreshes of a poll are queued.
var Kinds = []Kind{KindGlobalDropMatrix, KindGlobalPatternMatrix, KindShimTrend, KindSiteStats}

// storeTimeout bounds a read or an advance of the generations.
const storeTimeout = 5 * time.Second

func generationKey(kind Kind, server string) string {
	return "cachesync:gen:" + string(kind) + ":" + server
}

// Store holds the generations, shared by every process.
type Store interface {
	// Load returns the generations of keys, in their order: 0 for a key that does not exist (advanced generations
	// are 1 or more).
	Load(ctx context.Context, keys []string) ([]int64, error)
	// Advance sets the generation of key to the larger of one more than it is and floor, atomically, and returns the
	// one it was and the new one.
	Advance(ctx context.Context, key string, floor int64) (previous, generation int64, err error)
}

type redisStore struct {
	client *redis.Client
}

// advanceScript is Store.Advance: KEYS[1] is the key, ARGV[1] the floor. Lua numbers are doubles, exact up to 2^53,
// far above the milliseconds of any date.
var advanceScript = redis.NewScript(`
local previous = tonumber(redis.call('GET', KEYS[1]) or '0') or 0
local generation = math.max(previous + 1, tonumber(ARGV[1]))
redis.call('SET', KEYS[1], string.format('%d', generation))
return {previous, generation}
`)

func (s redisStore) Load(ctx context.Context, keys []string) ([]int64, error) {
	values, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	generations := make([]int64, len(keys))
	for i, v := range values {
		if v == nil { // the key does not exist
			continue
		}
		str, ok := v.(string)
		if !ok {
			return nil, errors.Errorf("cachesync: unexpected value %v of %s", v, keys[i])
		}
		if generations[i], err = strconv.ParseInt(str, 10, 64); err != nil {
			return nil, errors.Wrapf(err, "cachesync: unexpected value of %s", keys[i])
		}
	}
	return generations, nil
}

func (s redisStore) Advance(ctx context.Context, key string, floor int64) (previous, generation int64, err error) {
	values, err := advanceScript.Run(ctx, s.client, []string{key}, floor).Int64Slice()
	if err != nil {
		return 0, 0, err
	}
	if len(values) != 2 {
		return 0, 0, errors.Errorf("cachesync: unexpected reply %v advancing %s", values, key)
	}
	return values[0], values[1], nil
}

// follower refreshes the results of a kind of the servers the process serves, and drops those it does not.
type follower struct {
	// used reports whether the process serves any of the results of server that refresh replaces (see cache.Set.Used)
	used func(server string) bool
	// drop deletes the results of server that refresh replaces, those cached
	drop    func(server string)
	refresh func(ctx context.Context, server string) error
}

// target is the results of a kind of a server, which have a generation.
type target struct {
	kind   Kind
	server string
}

type Syncer struct {
	store Store
	// interval is the time between two polls; 0 disables them
	interval time.Duration
	// timeout bounds a refresh, as WorkerTimeout bounds those of the worker; 0 does not
	timeout   time.Duration
	followers map[Kind]follower
	// targets and keys are every target and the key of its generation, in the order they are polled
	targets []target
	keys    []string
	// now is the clock of the floors of Bump
	now func() time.Time

	// mu guards seen, unconfirmed, polled and late. A poll holds it from reading the generations until it recorded
	// them, and Bump from advancing one until it recorded it: a poll that read a generation before this process
	// advanced it does not record the older one after it, which would refresh what this process just refreshed, twice.
	mu sync.Mutex
	// seen holds the last generation of every target this process read or advanced
	seen map[target]int64
	// unconfirmed holds the floor of the bumps that failed, until the next poll: Redis may have applied one whose
	// reply was lost (e.g. a read timeout), and then set the generation to that floor
	unconfirmed map[target]int64
	// polled tells whether a poll succeeded since the process started, and late whether one failed before
	polled, late bool

	// refreshSem runs the refreshes of cached global results of the process one at a time: the followed ones, and
	// those of its worker (RefreshLock). A semaphore rather than a mutex, so that a followed refresh waiting for the
	// worker's stops waiting when the process stops.
	refreshSem semaphore

	// queueMu guards queue and queued, the targets waiting for a refresh, in order
	queueMu sync.Mutex
	queue   []target
	queued  map[target]bool
	// wake tells the refresh loop that a target was queued
	wake chan struct{}

	// pollFailing tells whether the last poll failed, so that a failure and the recovery are logged once rather than
	// every poll; only the poll loop uses it
	pollFailing bool
}

type Deps struct {
	fx.In

	Config               *appconfig.Config
	Redis                *redis.Client
	DropMatrixService    *service.DropMatrix
	PatternMatrixService *service.PatternMatrix
	TrendService         *service.Trend
	SiteStatsService     *service.SiteStats
}

// New returns the Syncer of the process, which calcwkr bumps the generations with, and which Start runs.
func New(deps Deps) *Syncer {
	return newSyncer(redisStore{deps.Redis}, deps.Config.CacheSyncInterval, deps.Config.WorkerTimeout, map[Kind]follower{
		KindGlobalDropMatrix: {
			deps.DropMatrixService.GlobalDropMatrixCachesUsed,
			deps.DropMatrixService.DropGlobalDropMatrixCaches,
			deps.DropMatrixService.RefreshGlobalDropMatrixCaches,
		},
		KindGlobalPatternMatrix: {
			deps.PatternMatrixService.GlobalPatternMatrixCachesUsed,
			deps.PatternMatrixService.DropGlobalPatternMatrixCaches,
			deps.PatternMatrixService.RefreshGlobalPatternMatrixCaches,
		},
		KindShimTrend: {deps.TrendService.ShimTrendUsed, deps.TrendService.DropShimTrend, deps.TrendService.RefreshShimTrend},
		KindSiteStats: {deps.SiteStatsService.ShimSiteStatsUsed, deps.SiteStatsService.DropShimSiteStats, func(ctx context.Context, server string) error {
			_, err := deps.SiteStatsService.RefreshShimSiteStats(ctx, server)
			return err
		}},
	}, constant.Servers)
}

func newSyncer(store Store, interval, timeout time.Duration, followers map[Kind]follower, servers []string) *Syncer {
	s := &Syncer{
		store:       store,
		interval:    interval,
		timeout:     timeout,
		followers:   followers,
		now:         time.Now,
		seen:        make(map[target]int64),
		unconfirmed: make(map[target]int64),
		queued:      make(map[target]bool),
		wake:        make(chan struct{}, 1),
		refreshSem:  make(semaphore, 1),
	}
	for _, kind := range Kinds {
		for _, server := range servers {
			s.targets = append(s.targets, target{kind, server})
			s.keys = append(s.keys, generationKey(kind, server))
		}
	}
	return s
}

// Start runs the polls and the refreshes they queue with the app, unless CacheSyncInterval is 0.
func Start(lc fx.Lifecycle, s *Syncer) error {
	if s.interval < 0 {
		return errors.New("cachesync: PENGUIN_V3_CACHE_SYNC_INTERVAL must not be negative")
	}
	if s.interval == 0 {
		log.Info().Str("evt.name", "worker.cachesync.disabled").
			Msg("cache sync is disabled due to configuration: the cached global results are only refreshed by the worker of this process")
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			wg.Add(2)
			go func() {
				defer wg.Done()
				s.pollLoop(ctx)
			}()
			go func() {
				defer wg.Done()
				s.refreshLoop(ctx)
			}()
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			// a refresh in progress is cancelled, and drops what it refreshes: requests recalculate it
			cancel()
			done := make(chan struct{})
			go func() {
				wg.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-stopCtx.Done():
			}
			return nil
		},
	})
	log.Info().Str("evt.name", "worker.cachesync.enabled").Dur("interval", s.interval).
		Msg("cache sync is enabled: the cached global results follow the refreshes of the worker of any process")
	return nil
}

// RefreshLock is the lock that runs the refreshes of cached global results of the process one at a time: those
// following the worker of another process take it, and the worker of this one takes it for its own, so that the
// process does not load the database with two at once, nor cache the results of one over those of a later one
// calculated from newer data.
func (s *Syncer) RefreshLock() sync.Locker {
	return s.refreshSem
}

// semaphore is a lock of one slot, which lockCtx can stop waiting for.
type semaphore chan struct{}

func (m semaphore) Lock()   { m <- struct{}{} }
func (m semaphore) Unlock() { <-m }

// lockCtx takes the lock unless ctx is done first, and reports whether it took it.
func (m semaphore) lockCtx(ctx context.Context) bool {
	select {
	case m <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// Bump tells every process that this one refreshed the results of kind of server, by advancing their generation.
// The new generation is recorded as seen, so that this process does not refresh them again, unless another process
// advanced it since this one last read it: the next poll then refreshes them, as the refresh of this process may have
// read the data before the other one's. A failure is logged and counted: unless Redis applied the advance
// nonetheless, its reply lost, the other processes keep their results until the next refresh of the worker.
func (s *Syncer) Bump(ctx context.Context, kind Kind, server string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	t := target{kind, server}
	floor := s.now().UnixMilli()
	previous, generation, err := s.store.Advance(ctx, generationKey(kind, server), floor)
	if err != nil {
		s.unconfirmed[t] = floor
		observability.CacheSyncBumps.WithLabelValues(string(kind), "error").Inc()
		log.Error().Str("evt.name", "worker.cachesync.bump").Str("kind", string(kind)).Str("server", server).Err(err).
			Msg("failed to advance the cache sync generation: unless Redis applied it nonetheless, other processes keep their cached results until the next refresh")
		return
	}
	observability.CacheSyncBumps.WithLabelValues(string(kind), "ok").Inc()
	delete(s.unconfirmed, t)
	// another process advanced it since this one read it (previous > seen): leave it to the next poll. Otherwise,
	// also before the first poll and after Redis lost it (previous < seen), the generation is this refresh's.
	if !s.polled || previous <= s.seen[t] {
		s.seen[t] = generation
	}
}

func (s *Syncer) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		s.pollAndLog(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Syncer) pollAndLog(ctx context.Context) {
	err := s.poll(ctx)
	if err != nil {
		if ctx.Err() != nil { // stopping
			return
		}
		observability.CacheSyncPolls.WithLabelValues("error").Inc()
		if !s.pollFailing {
			log.Warn().Str("evt.name", "worker.cachesync.poll").Err(err).
				Msg("failed to read the cache sync generations: retrying every interval, the cached global results are not refreshed meanwhile")
		}
		s.pollFailing = true
		return
	}
	observability.CacheSyncPolls.WithLabelValues("ok").Inc()
	if s.pollFailing {
		log.Info().Str("evt.name", "worker.cachesync.poll").Msg("read the cache sync generations again")
	}
	s.pollFailing = false
}

// poll reads every generation and queues a refresh of the results whose generation changed, if the process serves
// any of them, or drops them if it only caches them. The first poll only records the generations, as the process
// calculated what it caches since it started, unless a poll failed before it: the process may have cached results
// meanwhile, before a change it did not see. The first poll that succeeds then follows the changes from the
// generations this process bumped, and from 0 for the others, so that the results of a generation that ever changed
// are refreshed once.
func (s *Syncer) poll(ctx context.Context) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil && !s.polled {
			s.late = true
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	generations, err := s.store.Load(ctx, s.keys)
	if err != nil {
		return err
	}
	if len(generations) != len(s.keys) {
		return errors.Errorf("cachesync: read %d generations, want %d", len(generations), len(s.keys))
	}

	baseline := !s.polled && !s.late
	s.polled = true
	for i, t := range s.targets {
		generation, seen := generations[i], s.seen[t]
		s.seen[t] = generation
		if floor, ok := s.unconfirmed[t]; ok {
			delete(s.unconfirmed, t)
			if generation == floor {
				// the bump of this process that failed was applied, and is the last: what it refreshed is current. (Had
				// it failed to apply, the generation would be the floor only if another process advanced it to the same
				// millisecond.)
				continue
			}
		}
		if baseline || generation == seen {
			continue
		}
		if generation == 0 {
			// the generation is gone, as Redis lost its data (flushed, or failed over to a replica without it):
			// nothing tells that the results changed, and refreshing all of them in every process at once would load
			// the database. The next refresh of the worker advances it from 0, which is then a change. (A
			// generation that went back but not to 0, after a failover to a replica that missed advances, is a
			// change: that refreshes once. One that the worker advanced again before a poll saw it go back is past
			// the one lost, as it advances to the time of the refresh.)
			log.Warn().Str("evt.name", "worker.cachesync.poll").Str("kind", string(t.kind)).Str("server", t.server).
				Int64("seen", seen).Msg("the cache sync generation is gone: waiting for the next refresh of the worker")
			continue
		}
		if f := s.followers[t.kind]; !f.used(t.server) {
			// a request calculates them from the data of the refresh, if any comes
			f.drop(t.server)
			continue
		}
		s.enqueue(t)
	}
	return nil
}

// enqueue queues a refresh of t unless one is waiting already: it would read the same data. One that runs already
// may have read older data, and does not prevent another.
func (s *Syncer) enqueue(t target) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	if s.queued[t] {
		return
	}
	s.queued[t] = true
	s.queue = append(s.queue, t)
	select {
	case s.wake <- struct{}{}:
	default: // the refresh loop is woken already
	}
}

// next takes the first target of the queue, waiting for one until ctx is done.
func (s *Syncer) next(ctx context.Context) (target, bool) {
	for {
		s.queueMu.Lock()
		if len(s.queue) > 0 {
			t := s.queue[0]
			s.queue = s.queue[1:]
			delete(s.queued, t)
			s.queueMu.Unlock()
			return t, true
		}
		s.queueMu.Unlock()
		select {
		case <-ctx.Done():
			return target{}, false
		case <-s.wake:
		}
	}
}

// refreshLoop runs the queued refreshes one at a time, and one at a time with those of the worker of the process
// (RefreshLock): requests keep getting the previous results meanwhile, and the database gets one recalculation at a
// time.
func (s *Syncer) refreshLoop(ctx context.Context) {
	for {
		t, ok := s.next(ctx)
		if !ok {
			return
		}
		s.refresh(ctx, t)
	}
}

// refresh refreshes the results of t, as the worker did: if it fails, they are dropped, and requests recalculate them.
// One cancelled as the process stops is no failure: it is counted and logged as cancelled.
func (s *Syncer) refresh(stopCtx context.Context, t target) {
	if !s.refreshSem.lockCtx(stopCtx) { // stopped while waiting for the worker's refresh
		return
	}
	defer s.refreshSem.Unlock()
	if stopCtx.Err() != nil {
		return
	}
	kind := string(t.kind)
	logger := log.With().Str("evt.name", "worker.cachesync.refresh").Str("kind", kind).Str("server", t.server).Logger()
	ctx := logger.WithContext(stopCtx)
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	ctx, span := tracer.Start(ctx, "cachesync."+kind, trace.WithAttributes(attribute.String("server", t.server)))

	start := time.Now()
	var panicked bool
	err := func() (err error) {
		// this goroutine has no recovery: a panic would end the process
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				err = errors.Errorf("panic: %v\n%s", r, debug.Stack())
			}
		}()
		return s.followers[t.kind].refresh(ctx, t.server)
	}()
	dur := time.Since(start)
	endSpan(span, err)

	outcome := "ok"
	if panicked {
		outcome = "panic"
	} else if err != nil && stopCtx.Err() != nil {
		outcome = "cancelled"
	} else if err != nil {
		outcome = "error"
	}
	observability.CacheSyncRefreshDuration.WithLabelValues(kind, t.server).Observe(dur.Seconds())
	observability.CacheSyncRefreshes.WithLabelValues(kind, t.server, outcome).Inc()
	if outcome == "cancelled" {
		logger.Info().Err(err).Dur("took", dur).
			Msg("cancelled the refresh of the cached global results as the process stops: dropped them")
		return
	}
	if err != nil {
		logger.Error().Err(err).Dur("took", dur).
			Msg("failed to refresh the cached global results after the worker refreshed them: dropped them, requests recalculate them")
		return
	}
	logger.Info().Dur("took", dur).Msg("refreshed the cached global results after the worker refreshed them")
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
