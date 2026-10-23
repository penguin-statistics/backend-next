package calcwkr

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/go-redsync/redsync/v4"

	"exusiai.dev/backend-next/internal/workers/cachesync"
)

func TestGlobalMatrixRefreshRunsInTheBackgroundOneAtATime(t *testing.T) {
	w := &Worker{timeout: time.Minute, refreshGlobalMatrices: true}
	batchCtx, endBatch := context.WithCancel(context.Background())

	var (
		running, maxRunning atomic.Int32
		mu                  sync.Mutex
		refreshed           []string
		done                sync.WaitGroup
	)
	release := make(chan struct{})
	refresh := func(ctx context.Context, server string) error {
		defer done.Done()
		if n := running.Add(1); n > maxRunning.Load() {
			maxRunning.Store(n)
		}
		<-release
		if ctx.Err() != nil {
			t.Errorf("the refresh of %s was cancelled with the batch", server)
		}
		running.Add(-1)
		mu.Lock()
		refreshed = append(refreshed, server)
		mu.Unlock()
		return nil
	}

	done.Add(3)
	started := time.Now()
	for _, server := range []string{"CN", "US", "JP"} {
		w.globalMatrixRefresh(batchCtx, "globalDropMatrixRefresh", server, refresh)
	}
	if time.Since(started) > time.Second {
		t.Error("the batch waited for the refreshes")
	}
	endBatch() // the batch finishes before the refreshes
	close(release)
	done.Wait()

	if len(refreshed) != 3 {
		t.Errorf("refreshed %v, want every server", refreshed)
	}
	if maxRunning.Load() != 1 {
		t.Errorf("%d refreshes ran at the same time, want one", maxRunning.Load())
	}
}

func TestGlobalMatrixRefreshIsOffWithoutRequestsToServe(t *testing.T) {
	w := &Worker{timeout: time.Minute} // e.g. the workerbench worker
	w.globalMatrixRefresh(context.Background(), "globalDropMatrixRefresh", "CN", func(context.Context, string) error {
		t.Error("refreshed although disabled")
		return nil
	})
	time.Sleep(50 * time.Millisecond)
}

func TestGlobalMatrixRefreshRecoversFromAPanic(t *testing.T) {
	w := &Worker{timeout: time.Minute, refreshGlobalMatrices: true}
	var done sync.WaitGroup
	done.Add(2)
	w.globalMatrixRefresh(context.Background(), "globalPatternMatrixRefresh", "CN", func(context.Context, string) error {
		defer done.Done()
		var timesResult *struct{ Times int }
		_ = timesResult.Times // what an inconsistent read did
		return nil
	})
	// the process survived, and refreshes go on
	w.globalMatrixRefresh(context.Background(), "globalPatternMatrixRefresh", "US", func(context.Context, string) error {
		done.Done()
		return nil
	})
	done.Wait()
}

func TestGlobalCachesWarmUpRunsEveryServerOneAtATimeHoldingTheLocks(t *testing.T) {
	rs := newTestRedSync(t)
	w := newTestWorker(t, rs, 2*time.Second)
	w.timeout = time.Minute
	w.refreshGlobalMatrices = true
	if err := w.lock(); err != nil {
		t.Fatal(err)
	}
	defer w.unlock()

	var (
		running, maxRunning atomic.Int32
		mu                  sync.Mutex
		warmed              []string
		refreshedAfter      int
		refreshedDuring     bool
		refreshed           = make(chan struct{})
	)
	// the batch's context: the lock renewals of the microtasks end with it
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.warmUpGlobalCaches(ctx, func(ctx context.Context, server string) error {
		if n := running.Add(1); n > maxRunning.Load() {
			maxRunning.Store(n)
		}
		defer running.Add(-1)
		mu.Lock()
		warmed = append(warmed, server)
		mu.Unlock()
		switch server {
		case constant.Servers[0]:
			// a refresh scheduled meanwhile waits for the warm-up of the server (each holds the refresh lock), and
			// may run before the next server's
			w.globalMatrixRefresh(ctx, "globalDropMatrixRefresh", server, func(context.Context, string) error {
				mu.Lock()
				refreshedAfter = len(warmed)
				refreshedDuring = running.Load() > 0
				mu.Unlock()
				close(refreshed)
				return nil
			})
			return errors.New("failed")
		case constant.Servers[1]:
			var timesResult *struct{ Times int }
			_ = timesResult.Times // panics
		}
		// the warm-up takes longer than the lock's expiry
		time.Sleep(1300 * time.Millisecond)
		return nil
	})
	select {
	case <-refreshed:
	case <-time.After(10 * time.Second):
		t.Fatal("the refresh scheduled during the warm-up never ran")
	}

	if len(warmed) != len(constant.Servers) {
		t.Errorf("warmed up %v, want every server despite failures", warmed)
	}
	if maxRunning.Load() != 1 {
		t.Errorf("%d servers warmed up at the same time, want one", maxRunning.Load())
	}
	if refreshedDuring || refreshedAfter < 1 {
		t.Errorf("a refresh ran during a server's warm-up (after %d servers started), want between them", refreshedAfter)
	}
	other := rs.NewMutex(w.syncMutex.Name(), redsync.WithExpiry(time.Second), redsync.WithTries(1))
	if err := other.Lock(); err == nil {
		other.Unlock()
		t.Fatal("the worker lock expired during the warm-up")
	}
}

func TestGlobalCachesWarmUpIsOffWithoutRequestsToServe(t *testing.T) {
	w := &Worker{timeout: time.Minute} // e.g. the workerbench worker
	w.warmUpGlobalCaches(context.Background(), func(context.Context, string) error {
		t.Error("warmed up although disabled")
		return nil
	})
}

func TestGlobalCachesWarmUpRunsEveryWarmUpDespiteFailures(t *testing.T) {
	var warmed []string
	warmUp := func(name string, err error) cacheWarmUp {
		return cacheWarmUp{name, func(_ context.Context, server string) error {
			warmed = append(warmed, name+" "+server)
			return err
		}}
	}
	err := warmUpEach(context.Background(), "CN", []cacheWarmUp{
		warmUp("globalDropMatrix", errors.New("failed")), warmUp("globalPatternMatrix", nil), warmUp("shimTrend", errors.New("failed")),
	})
	if got, want := strings.Join(warmed, ", "), "globalDropMatrix CN, globalPatternMatrix CN, shimTrend CN"; got != want {
		t.Errorf("warmed up %s, want %s", got, want)
	}
	if err == nil || err.Error() != "failed to warm up globalDropMatrix, shimTrend" {
		t.Errorf("got error %v, want one naming every failed warm-up", err)
	}
	if err := warmUpEach(context.Background(), "CN", []cacheWarmUp{warmUp("globalDropMatrix", nil)}); err != nil {
		t.Errorf("got error %v, want none", err)
	}
}

// recordingBumper records the bumps of the generations of cached results.
type recordingBumper struct {
	mu     sync.Mutex
	bumped []string

	refreshMu sync.Mutex
}

func (b *recordingBumper) RefreshLock() sync.Locker { return &b.refreshMu }

func (b *recordingBumper) Bump(_ context.Context, kind cachesync.Kind, server string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bumped = append(b.bumped, string(kind)+" "+server)
}

func (b *recordingBumper) bumps() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.bumped, ", ")
}

func TestGlobalMatrixRefreshTellsTheOtherProcessesOnceItSucceeded(t *testing.T) {
	bumper := &recordingBumper{}
	w := &Worker{timeout: time.Minute, refreshGlobalMatrices: true, cacheSync: bumper}
	var done sync.WaitGroup
	done.Add(3)
	refresh := func(err error) func(context.Context, string) error {
		return func(context.Context, string) error {
			defer done.Done()
			return err
		}
	}
	w.globalMatrixRefresh(context.Background(), "globalDropMatrixRefresh", "CN", w.bumping(cachesync.KindGlobalDropMatrix, refresh(nil)))
	// a failed refresh dropped the results: the other processes keep theirs until the next refresh
	w.globalMatrixRefresh(context.Background(), "shimTrendRefresh", "US", w.bumping(cachesync.KindShimTrend, refresh(errors.New("failed"))))
	w.globalMatrixRefresh(context.Background(), "globalPatternMatrixRefresh", "JP", w.bumping(cachesync.KindGlobalPatternMatrix, func(context.Context, string) error {
		defer done.Done()
		var timesResult *struct{ Times int }
		_ = timesResult.Times // panics
		return nil
	}))
	done.Wait()
	// every refresh ran, holding the refresh lock of the cache sync: the last one bumped by the time it releases it
	bumper.refreshMu.Lock()
	defer bumper.refreshMu.Unlock()

	if got, want := bumper.bumps(), "globalDropMatrix CN"; got != want {
		t.Errorf("bumped %q, want %q", got, want)
	}
}

func TestBumpWithoutCacheSync(t *testing.T) {
	w := &Worker{} // e.g. the workerbench worker
	if err := w.bumping(cachesync.KindSiteStats, func(context.Context, string) error { return nil })(context.Background(), "CN"); err != nil {
		t.Fatal(err)
	}
}

// The refreshes of the worker take the refresh lock of the cache sync, which its refreshes following the worker of
// another process take too: the process runs one at a time.
func TestGlobalMatrixRefreshWaitsForAFollowedRefresh(t *testing.T) {
	bumper := &recordingBumper{}
	w := &Worker{timeout: time.Minute, refreshGlobalMatrices: true, cacheSync: bumper}
	bumper.refreshMu.Lock() // a followed refresh runs
	refreshed := make(chan struct{})
	w.globalMatrixRefresh(context.Background(), "shimTrendRefresh", "CN", func(context.Context, string) error {
		close(refreshed)
		return nil
	})
	select {
	case <-refreshed:
		t.Fatal("refreshed while a followed refresh runs")
	case <-time.After(50 * time.Millisecond):
	}
	bumper.refreshMu.Unlock()
	<-refreshed
}
