package cachesync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/pkg/cache"
	"exusiai.dev/backend-next/internal/pkg/observability"
)

// memoryStore stands in for the generations shared through Redis: a missing key is 0, as with MGET.
type memoryStore struct {
	mu          sync.Mutex
	generations map[string]int64
	err         error
	loads       int
}

func (s *memoryStore) Load(_ context.Context, keys []string) ([]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	s.loads++
	generations := make([]int64, len(keys))
	for i, key := range keys {
		generations[i] = s.generations[key]
	}
	return generations, nil
}

func (s *memoryStore) Advance(_ context.Context, key string, floor int64) (previous, generation int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return 0, 0, s.err
	}
	if s.generations == nil {
		s.generations = make(map[string]int64)
	}
	previous = s.generations[key]
	s.generations[key] = max(previous+1, floor)
	return previous, s.generations[key], nil
}

func (s *memoryStore) get(kind Kind, server string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generations[generationKey(kind, server)]
}

func (s *memoryStore) set(kind Kind, server string, generation int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generations == nil {
		s.generations = make(map[string]int64)
	}
	s.generations[generationKey(kind, server)] = generation
}

func (s *memoryStore) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generations = nil
}

func (s *memoryStore) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

// fakeCaches is what the process caches and serves (uses), per target, and the refreshes and drops it ran.
type fakeCaches struct {
	mu        sync.Mutex
	cached    map[target]bool
	used      map[target]bool
	refreshed []target
	dropped   []target
}

// use caches the results of kind of server, and serves them.
func (c *fakeCaches) use(kind Kind, server string) {
	c.cacheUnused(kind, server)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.used[target{kind, server}] = true
}

// cacheUnused caches the results of kind of server without serving them, as the worker's own refreshes do.
func (c *fakeCaches) cacheUnused(kind Kind, server string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached == nil {
		c.cached, c.used = make(map[target]bool), make(map[target]bool)
	}
	c.cached[target{kind, server}] = true
}

func (c *fakeCaches) refreshes() []target {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]target(nil), c.refreshed...)
}

func (c *fakeCaches) drops() []target {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]target(nil), c.dropped...)
}

func (c *fakeCaches) followers() map[Kind]follower {
	followers := make(map[Kind]follower)
	for _, kind := range Kinds {
		kind := kind
		followers[kind] = follower{
			used: func(server string) bool {
				c.mu.Lock()
				defer c.mu.Unlock()
				return c.used[target{kind, server}]
			},
			drop: func(server string) {
				c.mu.Lock()
				defer c.mu.Unlock()
				t := target{kind, server}
				if c.cached[t] {
					c.dropped = append(c.dropped, t)
				}
				delete(c.cached, t)
				delete(c.used, t)
			},
			refresh: func(_ context.Context, server string) error {
				c.mu.Lock()
				defer c.mu.Unlock()
				c.refreshed = append(c.refreshed, target{kind, server})
				return nil
			},
		}
	}
	return followers
}

var testServers = []string{"CN", "US"}

// newTestSyncer returns a Syncer whose clock stays at the epoch: its bumps advance the generations by one.
func newTestSyncer(store Store, caches *fakeCaches) *Syncer {
	s := newSyncer(store, time.Second, time.Minute, caches.followers(), testServers)
	s.now = func() time.Time { return time.UnixMilli(0) }
	return s
}

// queuedTargets returns the targets waiting for a refresh, in order.
func (s *Syncer) queuedTargets() []target {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return append([]target(nil), s.queue...)
}

func mustPoll(t *testing.T, s *Syncer) {
	t.Helper()
	if err := s.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func assertQueued(t *testing.T, s *Syncer, want ...target) {
	t.Helper()
	got := s.queuedTargets()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("queued %v, want %v", got, want)
	}
}

func TestFirstPollOnlyRecordsTheGenerations(t *testing.T) {
	store := &memoryStore{}
	store.set(KindGlobalDropMatrix, "CN", 7)
	store.set(KindSiteStats, "US", 3)
	caches := &fakeCaches{}
	caches.use(KindGlobalDropMatrix, "CN")
	caches.use(KindSiteStats, "US")
	s := newTestSyncer(store, caches)

	// the process calculated what it caches since it started
	mustPoll(t, s)
	assertQueued(t, s)

	// unchanged generations refresh nothing
	mustPoll(t, s)
	assertQueued(t, s)
}

func TestPollRefreshesTheChangedResultsTheProcessCaches(t *testing.T) {
	store := &memoryStore{}
	caches := &fakeCaches{}
	caches.use(KindGlobalDropMatrix, "CN")
	caches.use(KindShimTrend, "CN")
	caches.use(KindShimTrend, "US")
	s := newTestSyncer(store, caches)
	mustPoll(t, s)

	store.set(KindGlobalDropMatrix, "CN", 1)
	store.set(KindShimTrend, "US", 1)
	// changed, but not cached here: a request calculates them from the new data, if any comes
	store.set(KindGlobalPatternMatrix, "CN", 1)
	store.set(KindSiteStats, "US", 1)
	mustPoll(t, s)
	assertQueued(t, s, target{KindGlobalDropMatrix, "CN"}, target{KindShimTrend, "US"})
	if dropped := caches.drops(); len(dropped) != 0 {
		t.Errorf("dropped %v, which were not cached", dropped)
	}

	// the generations are recorded although nothing was refreshed: results cached later are calculated after the
	// change, and are not refreshed for it
	caches.use(KindGlobalPatternMatrix, "CN")
	caches.use(KindSiteStats, "US")
	mustPoll(t, s)
	assertQueued(t, s, target{KindGlobalDropMatrix, "CN"}, target{KindShimTrend, "US"})
}

func TestARefreshIsQueuedOnce(t *testing.T) {
	store := &memoryStore{}
	caches := &fakeCaches{}
	caches.use(KindGlobalDropMatrix, "CN")
	s := newTestSyncer(store, caches)
	mustPoll(t, s)

	// two changes before the refresh starts: a single refresh reads the data of both
	store.set(KindGlobalDropMatrix, "CN", 1)
	mustPoll(t, s)
	store.set(KindGlobalDropMatrix, "CN", 2)
	mustPoll(t, s)
	assertQueued(t, s, target{KindGlobalDropMatrix, "CN"})

	// a change once the refresh started may be newer than the data it read: it is queued again
	if tg, ok := s.next(context.Background()); !ok || tg != (target{KindGlobalDropMatrix, "CN"}) {
		t.Fatalf("took %v, want the queued refresh", tg)
	}
	store.set(KindGlobalDropMatrix, "CN", 3)
	mustPoll(t, s)
	assertQueued(t, s, target{KindGlobalDropMatrix, "CN"})
}

func TestBumpDoesNotRefreshTheProcessThatRefreshed(t *testing.T) {
	store := &memoryStore{}
	caches := &fakeCaches{}
	caches.use(KindGlobalPatternMatrix, "US")
	s := newTestSyncer(store, caches)
	mustPoll(t, s)

	s.Bump(context.Background(), KindGlobalPatternMatrix, "US")
	if got := store.generations[generationKey(KindGlobalPatternMatrix, "US")]; got != 1 {
		t.Fatalf("generation %d after a bump, want 1", got)
	}
	mustPoll(t, s)
	assertQueued(t, s)

	// another process refreshes them: this one follows
	other := newTestSyncer(store, &fakeCaches{})
	other.Bump(context.Background(), KindGlobalPatternMatrix, "US")
	mustPoll(t, s)
	assertQueued(t, s, target{KindGlobalPatternMatrix, "US"})
}

func TestBumpBeforeTheFirstPoll(t *testing.T) {
	store := &memoryStore{}
	caches := &fakeCaches{}
	caches.use(KindSiteStats, "CN")
	s := newTestSyncer(store, caches)

	// the worker of the process refreshed before the first poll
	s.Bump(context.Background(), KindSiteStats, "CN")
	mustPoll(t, s)
	mustPoll(t, s)
	assertQueued(t, s)
}

// A bump after Redis lost the generation, which this process had seen higher, is this process's refresh: what it
// just cached is not dropped as results of another process's refresh that nobody read.
func TestBumpAfterRedisLostTheGenerationIsSeen(t *testing.T) {
	store := &memoryStore{}
	caches := &fakeCaches{}
	caches.cacheUnused(KindGlobalDropMatrix, "US")
	s := newTestSyncer(store, caches)
	mustPoll(t, s)
	s.Bump(context.Background(), KindGlobalDropMatrix, "US")
	s.Bump(context.Background(), KindGlobalDropMatrix, "US")
	mustPoll(t, s) // seen 2

	store.flush() // the next bump advances it to 1, below the one seen
	s.Bump(context.Background(), KindGlobalDropMatrix, "US")
	mustPoll(t, s)
	assertQueued(t, s)
	if dropped := caches.drops(); len(dropped) != 0 {
		t.Errorf("dropped %v that this process just refreshed", dropped)
	}
}

func TestBumpFailureIsCounted(t *testing.T) {
	store := &memoryStore{}
	store.setErr(errors.New("redis is down"))
	s := newTestSyncer(store, &fakeCaches{})
	failures := observability.CacheSyncBumps.WithLabelValues(string(KindShimTrend), "error")
	before := testutil.ToFloat64(failures)

	s.Bump(context.Background(), KindShimTrend, "CN")

	if got := testutil.ToFloat64(failures) - before; got != 1 {
		t.Errorf("counted %v failed bumps, want 1", got)
	}
}

func TestMissingGenerationsRefreshNothing(t *testing.T) {
	store := &memoryStore{}
	caches := &fakeCaches{}
	for _, kind := range Kinds {
		for _, server := range testServers {
			store.set(kind, server, 5)
			caches.use(kind, server)
		}
	}
	s := newTestSyncer(store, caches)
	mustPoll(t, s)

	// Redis lost its data: no refresh of everything at once, and no error
	store.flush()
	mustPoll(t, s)
	mustPoll(t, s)
	assertQueued(t, s)

	// the next refresh of the worker increments a generation from 0 again: that is a change
	newTestSyncer(store, &fakeCaches{}).Bump(context.Background(), KindShimTrend, "US")
	mustPoll(t, s)
	assertQueued(t, s, target{KindShimTrend, "US"})
}

func TestAGenerationThatWentBackIsAChange(t *testing.T) {
	store := &memoryStore{}
	caches := &fakeCaches{}
	caches.use(KindGlobalDropMatrix, "US")
	store.set(KindGlobalDropMatrix, "US", 9)
	s := newTestSyncer(store, caches)
	mustPoll(t, s)

	// failed over to a replica that missed increments
	store.set(KindGlobalDropMatrix, "US", 8)
	mustPoll(t, s)
	assertQueued(t, s, target{KindGlobalDropMatrix, "US"})
}

func TestAFailedPollKeepsTheSeenGenerations(t *testing.T) {
	store := &memoryStore{}
	caches := &fakeCaches{}
	caches.use(KindSiteStats, "US")
	s := newTestSyncer(store, caches)
	mustPoll(t, s)

	// the changes made while Redis was unreachable are seen after it
	store.setErr(errors.New("redis is down"))
	if err := s.poll(context.Background()); err == nil {
		t.Fatal("poll succeeded without the generations")
	}
	store.setErr(nil)
	store.set(KindSiteStats, "US", 5)
	mustPoll(t, s)
	assertQueued(t, s, target{KindSiteStats, "US"})
}

// A bump over another process's before the first poll succeeded (after failed ones) is not taken for this process's
// own: what this process refreshed may have read the data before the other one's, and is refreshed again.
func TestABumpOverAnotherProcessesBeforeTheFirstPollIsFollowed(t *testing.T) {
	store := &memoryStore{}
	caches := &fakeCaches{}
	caches.use(KindGlobalDropMatrix, "CN")
	s := newTestSyncer(store, caches)
	store.setErr(errors.New("redis is down"))
	if err := s.poll(context.Background()); err == nil {
		t.Fatal("poll succeeded without the generations")
	}
	store.setErr(nil)

	other := newTestSyncer(store, &fakeCaches{})
	other.Bump(context.Background(), KindGlobalDropMatrix, "CN")
	s.Bump(context.Background(), KindGlobalDropMatrix, "CN")
	mustPoll(t, s)
	assertQueued(t, s, target{KindGlobalDropMatrix, "CN"})
}

func TestAFirstPollAfterFailedOnesFollowsWhatTheProcessCached(t *testing.T) {
	store := &memoryStore{}
	store.set(KindSiteStats, "US", 3)
	caches := &fakeCaches{}
	s := newTestSyncer(store, caches)

	// a process that cannot read the generations at first serves requests meanwhile
	store.setErr(errors.New("redis is down"))
	if err := s.poll(context.Background()); err == nil {
		t.Fatal("poll succeeded without the generations")
	}
	caches.use(KindSiteStats, "US")
	caches.use(KindShimTrend, "CN")
	caches.cacheUnused(KindGlobalDropMatrix, "CN")
	caches.use(KindGlobalPatternMatrix, "CN")
	// and the worker of another process refreshes some, which it does not see
	store.set(KindSiteStats, "US", 4)
	store.set(KindGlobalDropMatrix, "CN", 1)
	// the worker of this one refreshed the pattern matrices once Redis was back: they are current
	store.setErr(nil)
	s.Bump(context.Background(), KindGlobalPatternMatrix, "CN")

	// what it caches may predate a change: what it serves is refreshed, the rest dropped; the generation of the trend
	// never changed
	mustPoll(t, s)
	assertQueued(t, s, target{KindSiteStats, "US"})
	if dropped := caches.drops(); fmt.Sprint(dropped) != fmt.Sprint([]target{{KindGlobalDropMatrix, "CN"}}) {
		t.Errorf("dropped %v, want the drop matrices only", dropped)
	}
}

func TestBumpOverAnotherProcessesBumpRefreshesAtTheNextPoll(t *testing.T) {
	store := &memoryStore{}
	caches := &fakeCaches{}
	caches.use(KindShimTrend, "CN")
	s := newTestSyncer(store, caches)
	mustPoll(t, s)

	// another process refreshed after this one last read the generation, maybe from newer data than this one's refresh
	newTestSyncer(store, &fakeCaches{}).Bump(context.Background(), KindShimTrend, "CN")
	s.Bump(context.Background(), KindShimTrend, "CN")
	mustPoll(t, s)
	assertQueued(t, s, target{KindShimTrend, "CN"})
}

// lostReplyStore applies the advances, then fails as a reply that timed out does.
type lostReplyStore struct {
	*memoryStore
}

func (s lostReplyStore) Advance(ctx context.Context, key string, floor int64) (int64, int64, error) {
	if _, _, err := s.memoryStore.Advance(ctx, key, floor); err != nil {
		return 0, 0, err
	}
	return 0, 0, errors.New("i/o timeout")
}

func TestAFailedBumpThatWasAppliedDoesNotRefreshTheProcessThatRefreshed(t *testing.T) {
	store := &memoryStore{}
	store.set(KindGlobalDropMatrix, "CN", 1000)
	caches := &fakeCaches{}
	caches.use(KindGlobalDropMatrix, "CN")
	caches.use(KindShimTrend, "CN")
	s := newTestSyncer(lostReplyStore{store}, caches)
	clock := int64(2000)
	s.now = func() time.Time { return time.UnixMilli(clock) }
	mustPoll(t, s)

	s.Bump(context.Background(), KindGlobalDropMatrix, "CN")
	mustPoll(t, s)
	assertQueued(t, s)

	// another process advanced it after this one: that is a change
	s.Bump(context.Background(), KindShimTrend, "CN")
	newTestSyncer(store, &fakeCaches{}).Bump(context.Background(), KindShimTrend, "CN")
	mustPoll(t, s)
	assertQueued(t, s, target{KindShimTrend, "CN"})
}

func TestAGenerationRedisLostIsNotReachedAgain(t *testing.T) {
	store := &memoryStore{}
	caches := &fakeCaches{}
	caches.use(KindShimTrend, "CN")
	caches.use(KindSiteStats, "US")
	s := newTestSyncer(store, caches)
	worker := newTestSyncer(store, &fakeCaches{})
	clock := int64(1000)
	worker.now = func() time.Time { return time.UnixMilli(clock) }
	worker.Bump(context.Background(), KindShimTrend, "CN")
	worker.Bump(context.Background(), KindSiteStats, "US")
	mustPoll(t, s)

	// Redis fails over to a replica that missed the last advance of the trend, and loses the site stats (flushed):
	// the worker advances both again before this process polls
	clock = 5000
	store.set(KindShimTrend, "CN", 999)
	store.set(KindSiteStats, "US", 0)
	worker.Bump(context.Background(), KindShimTrend, "CN")
	worker.Bump(context.Background(), KindSiteStats, "US")
	if got := store.get(KindShimTrend, "CN"); got != 5000 {
		t.Fatalf("advanced the generation to %d, want the time of the bump", got)
	}
	mustPoll(t, s)
	assertQueued(t, s, target{KindShimTrend, "CN"}, target{KindSiteStats, "US"})
}

func TestResultsCachedButNotServedAreDropped(t *testing.T) {
	store := &memoryStore{}
	caches := &fakeCaches{}
	// e.g. a worker that refreshed them for no request
	caches.cacheUnused(KindGlobalDropMatrix, "CN")
	caches.cacheUnused(KindSiteStats, "US")
	s := newTestSyncer(store, caches)
	mustPoll(t, s)

	store.set(KindGlobalDropMatrix, "CN", 1)
	store.set(KindSiteStats, "US", 1)
	mustPoll(t, s)
	assertQueued(t, s)
	if dropped := caches.drops(); fmt.Sprint(dropped) != fmt.Sprint([]target{{KindGlobalDropMatrix, "CN"}, {KindSiteStats, "US"}}) {
		t.Errorf("dropped %v, want both", dropped)
	}
}

// A request calculating results the worker of another process refreshes meanwhile may calculate them from the data
// before: the process follows the refresh, which keeps the request's results from being cached.
// Results this process does not serve are refreshed rather than dropped while it refreshes cached results: that
// refresh may have read the data before the change, and would cache it after the drop.
func TestAChangeDuringARefreshOfTheProcessIsRefreshedAfterIt(t *testing.T) {
	store := &memoryStore{}
	caches := &fakeCaches{}
	caches.cacheUnused(KindGlobalDropMatrix, "CN")
	s := newTestSyncer(store, caches)
	mustPoll(t, s)

	lock := s.RefreshLock()
	lock.Lock() // the worker of the process refreshes
	newTestSyncer(store, &fakeCaches{}).Bump(context.Background(), KindGlobalDropMatrix, "CN")
	mustPoll(t, s)
	lock.Unlock()
	assertQueued(t, s, target{KindGlobalDropMatrix, "CN"})
	if dropped := caches.drops(); len(dropped) != 0 {
		t.Errorf("dropped %v while the process refreshed", dropped)
	}
}

func TestACalculationInFlightIsRefreshed(t *testing.T) {
	trends := cache.NewSet[int]("cachesyncTestTrend")
	followers := map[Kind]follower{
		KindShimTrend: {
			used: trends.Used,
			drop: func(server string) { _ = trends.Delete(server) },
			refresh: func(_ context.Context, server string) error {
				trends.Set(server, 2, time.Minute) // from the data of the refresh
				return nil
			},
		},
	}
	store := &memoryStore{}
	s := newSyncer(store, time.Second, time.Minute, followers, []string{"CN"})
	mustPoll(t, s)

	started, release, result := make(chan struct{}), make(chan struct{}), make(chan int)
	go func() {
		var v int
		_, _ = trends.MutexGetSet("CN", &v, func() (*int, error) {
			close(started)
			<-release
			stale := 1 // from the data before the refresh
			return &stale, nil
		}, time.Minute)
		result <- v
	}()
	<-started
	store.set(KindShimTrend, "CN", 1)
	mustPoll(t, s)
	assertQueued(t, s, target{KindShimTrend, "CN"})
	tg, _ := s.next(context.Background())
	s.refresh(context.Background(), tg)
	close(release)

	if v := <-result; v != 2 {
		t.Errorf("the request got %d, want the refreshed 2", v)
	}
	var cached int
	if err := trends.Get("CN", &cached); err != nil || cached != 2 {
		t.Errorf("cached %d (%v), want the refreshed 2", cached, err)
	}
}

func TestRefreshWaitsForTheRefreshOfTheWorker(t *testing.T) {
	caches := &fakeCaches{}
	s := newTestSyncer(&memoryStore{}, caches)
	lock := s.RefreshLock()
	lock.Lock() // the worker of the process refreshes
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.refresh(context.Background(), target{KindGlobalDropMatrix, "CN"})
	}()
	time.Sleep(50 * time.Millisecond)
	if refreshed := caches.refreshes(); len(refreshed) != 0 {
		t.Errorf("refreshed %v while the worker refreshes", refreshed)
	}
	lock.Unlock()
	<-done
	if refreshed := caches.refreshes(); len(refreshed) != 1 {
		t.Errorf("refreshed %v, want the drop matrices", refreshed)
	}
}

// A refresh waiting for the worker's stops waiting when the process stops, instead of holding up the shutdown.
func TestARefreshStopsWaitingForTheWorkerWhenTheProcessStops(t *testing.T) {
	caches := &fakeCaches{}
	s := newTestSyncer(&memoryStore{}, caches)
	lock := s.RefreshLock()
	lock.Lock() // the worker of the process refreshes
	defer lock.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.refresh(ctx, target{KindGlobalDropMatrix, "CN"})
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh kept waiting for the worker's after the process stopped")
	}
	if refreshed := caches.refreshes(); len(refreshed) != 0 {
		t.Errorf("refreshed %v after the process stopped", refreshed)
	}
}

func TestPollsAreCounted(t *testing.T) {
	store := &memoryStore{}
	s := newTestSyncer(store, &fakeCaches{})
	ok, failed := observability.CacheSyncPolls.WithLabelValues("ok"), observability.CacheSyncPolls.WithLabelValues("error")
	okBefore, failedBefore := testutil.ToFloat64(ok), testutil.ToFloat64(failed)

	s.pollAndLog(context.Background())
	store.setErr(errors.New("redis is down"))
	s.pollAndLog(context.Background())
	s.pollAndLog(context.Background())
	if !s.pollFailing {
		t.Error("the failing polls are not recorded as failing")
	}
	store.setErr(nil)
	s.pollAndLog(context.Background())
	if s.pollFailing {
		t.Error("a poll that succeeded is recorded as failing")
	}

	if got := testutil.ToFloat64(ok) - okBefore; got != 2 {
		t.Errorf("counted %v polls ok, want 2", got)
	}
	if got := testutil.ToFloat64(failed) - failedBefore; got != 2 {
		t.Errorf("counted %v failed polls, want 2", got)
	}
}

func TestRefreshesRunOneAtATime(t *testing.T) {
	var (
		running, maxRunning atomic.Int32
		mu                  sync.Mutex
		refreshed           []target
		done                sync.WaitGroup
	)
	release := make(chan struct{})
	followers := make(map[Kind]follower)
	for _, kind := range Kinds {
		kind := kind
		followers[kind] = follower{
			used: func(string) bool { return true },
			refresh: func(ctx context.Context, server string) error {
				defer done.Done()
				if n := running.Add(1); n > maxRunning.Load() {
					maxRunning.Store(n)
				}
				<-release
				running.Add(-1)
				mu.Lock()
				refreshed = append(refreshed, target{kind, server})
				mu.Unlock()
				return nil
			},
		}
	}
	s := newSyncer(&memoryStore{}, time.Second, time.Minute, followers, testServers)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.refreshLoop(ctx)

	done.Add(3)
	s.enqueue(target{KindGlobalDropMatrix, "CN"})
	s.enqueue(target{KindShimTrend, "CN"})
	s.enqueue(target{KindSiteStats, "US"})
	close(release)
	done.Wait()

	want := []target{{KindGlobalDropMatrix, "CN"}, {KindShimTrend, "CN"}, {KindSiteStats, "US"}}
	if fmt.Sprint(refreshed) != fmt.Sprint(want) {
		t.Errorf("refreshed %v, want %v", refreshed, want)
	}
	if maxRunning.Load() != 1 {
		t.Errorf("%d refreshes ran at the same time, want one", maxRunning.Load())
	}
}

func TestRefreshRecordsItsOutcomeAndRecoversFromAPanic(t *testing.T) {
	errFailed := errors.New("failed")
	stopCtx, stop := context.WithCancel(context.Background())
	defer stop()
	followers := map[Kind]follower{
		KindGlobalDropMatrix: {refresh: func(context.Context, string) error { return nil }},
		KindShimTrend:        {refresh: func(context.Context, string) error { return errFailed }},
		KindSiteStats: {refresh: func(context.Context, string) error {
			var stats *struct{ Total int }
			_ = stats.Total
			return nil
		}},
		KindGlobalPatternMatrix: {refresh: func(ctx context.Context, _ string) error {
			stop() // the process stops during the refresh
			<-ctx.Done()
			return ctx.Err()
		}},
	}
	s := newSyncer(&memoryStore{}, time.Second, time.Minute, followers, testServers)
	for _, tc := range []struct {
		kind    Kind
		outcome string
	}{
		{KindGlobalDropMatrix, "ok"},
		{KindShimTrend, "error"},
		{KindSiteStats, "panic"},
		{KindGlobalPatternMatrix, "cancelled"},
	} {
		refreshes := observability.CacheSyncRefreshes.WithLabelValues(string(tc.kind), "JP", tc.outcome)
		before := testutil.ToFloat64(refreshes)
		failures := observability.CacheSyncRefreshes.WithLabelValues(string(tc.kind), "JP", "error")
		failuresBefore := testutil.ToFloat64(failures)
		durations := observability.CacheSyncRefreshDuration.WithLabelValues(string(tc.kind), "JP")
		durationsBefore := sampleCount(t, durations)

		s.refresh(stopCtx, target{tc.kind, "JP"})

		if got := testutil.ToFloat64(refreshes) - before; got != 1 {
			t.Errorf("%s: counted %v refreshes with outcome %s, want 1", tc.kind, got, tc.outcome)
		}
		if got := sampleCount(t, durations) - durationsBefore; got != 1 {
			t.Errorf("%s: observed %d durations, want 1", tc.kind, got)
		}
		if tc.outcome != "error" && testutil.ToFloat64(failures) != failuresBefore {
			t.Errorf("%s: counted a failed refresh", tc.kind)
		}
	}

	// a refresh that waited for the worker's until the process stopped does not run
	followers[KindShimTrend] = follower{refresh: func(context.Context, string) error {
		t.Error("refreshed after the process stopped")
		return nil
	}}
	s.refresh(stopCtx, target{KindShimTrend, "JP"})
}

// sampleCount returns how many observations the histogram o has.
func sampleCount(t *testing.T, o prometheus.Observer) uint64 {
	t.Helper()
	var m dto.Metric
	if err := o.(prometheus.Metric).Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram().GetSampleCount()
}

// hookRecorder is an fx.Lifecycle that records the hooks appended to it.
type hookRecorder struct {
	hooks []fx.Hook
}

func (r *hookRecorder) Append(h fx.Hook) { r.hooks = append(r.hooks, h) }

func TestStartFollowsThePolledChanges(t *testing.T) {
	store := &memoryStore{}
	caches := &fakeCaches{}
	caches.use(KindGlobalPatternMatrix, "CN")
	s := newSyncer(store, 10*time.Millisecond, time.Minute, caches.followers(), testServers)
	lc := &hookRecorder{}
	if err := Start(lc, s); err != nil {
		t.Fatal(err)
	}
	if len(lc.hooks) != 1 {
		t.Fatalf("appended %d hooks, want 1", len(lc.hooks))
	}
	if err := lc.hooks[0].OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lc.hooks[0].OnStop(context.Background()); err != nil {
			t.Error(err)
		}
	}()

	// the first poll runs in OnStart, before the hooks after it (the HTTP server's) start
	store.mu.Lock()
	loads := store.loads
	store.mu.Unlock()
	if loads != 1 {
		t.Fatalf("%d polls when OnStart returned, want the first one", loads)
	}
	store.set(KindGlobalPatternMatrix, "CN", 1)
	waitFor(t, "the refresh", func() bool { return len(caches.refreshes()) == 1 })
	if got := caches.refreshes()[0]; got != (target{KindGlobalPatternMatrix, "CN"}) {
		t.Errorf("refreshed %v", got)
	}
}

func TestStartDisabled(t *testing.T) {
	store := &memoryStore{}
	lc := &hookRecorder{}
	if err := Start(lc, newSyncer(store, 0, time.Minute, (&fakeCaches{}).followers(), testServers)); err != nil {
		t.Fatal(err)
	}
	if len(lc.hooks) != 0 {
		t.Errorf("the disabled cache sync appended %d hooks, want none", len(lc.hooks))
	}
	if err := Start(lc, newSyncer(store, -time.Second, time.Minute, (&fakeCaches{}).followers(), testServers)); err == nil {
		t.Error("started with a negative interval")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestRedisStore(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()
	keys := []string{generationKey(KindShimTrend, "CN"), generationKey(KindShimTrend, "US")}

	store := redisStore{client}
	generations, err := store.Load(ctx, keys)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(generations) != "[0 0]" {
		t.Errorf("loaded %v for missing keys, want [0 0]", generations)
	}
	const now = 1761234567890 // milliseconds, as Bump's floor
	for _, tc := range []struct {
		floor, previous, generation int64
	}{
		{0, 0, 1},                   // one more
		{0, 1, 2},                   // one more
		{now, 2, now},               // the floor, which is more
		{now, now, now + 1},         // one more than the floor, in the same millisecond
		{now - 5, now + 1, now + 2}, // a clock behind does not go back
	} {
		previous, generation, err := store.Advance(ctx, keys[1], tc.floor)
		if err != nil || previous != tc.previous || generation != tc.generation {
			t.Fatalf("advanced from %d to %d (%v) with floor %d, want from %d to %d", previous, generation, err, tc.floor, tc.previous, tc.generation)
		}
	}
	if generations, err = store.Load(ctx, keys); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(generations) != fmt.Sprint([]int64{0, now + 2}) {
		t.Errorf("loaded %v, want [0 %d]", generations, now+2)
	}

	// Redis lost the generations: they start again from the time of the bump, not from 1
	mr.FlushAll()
	if _, generation, err := store.Advance(ctx, keys[1], now+10); err != nil || generation != now+10 {
		t.Errorf("advanced a lost generation to %d (%v), want the floor", generation, err)
	}

	mr.Set(keys[0], "x")
	if _, err := store.Load(ctx, keys); err == nil {
		t.Error("loaded a generation that is no number")
	}
}
