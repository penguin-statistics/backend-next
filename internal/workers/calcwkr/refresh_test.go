package calcwkr

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
