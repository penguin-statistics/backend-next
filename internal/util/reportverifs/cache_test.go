package reportverifs

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/errors"

	"exusiai.dev/backend-next/internal/pkg/pgerr"
)

func TestExpiringCacheExpiry(t *testing.T) {
	var c expiringCache[string, int]
	ctx := context.Background()
	t0 := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	loads := 0
	load := func(value int, expiry time.Time) func() (int, time.Time, error) {
		return func() (int, time.Time, error) {
			loads++
			return value, expiry, nil
		}
	}

	get := func(now time.Time, value int, expiry time.Time) int {
		t.Helper()
		got, err := c.get(ctx, now, "k", load(value, expiry))
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := get(t0, 1, t0.Add(time.Minute)); got != 1 || loads != 1 {
		t.Fatalf("first get: %d after %d loads, want 1 after 1", got, loads)
	}
	// cached until just before its expiry
	if got := get(t0.Add(time.Minute-time.Nanosecond), 2, t0.Add(2*time.Minute)); got != 1 || loads != 1 {
		t.Fatalf("get before the expiry: %d after %d loads, want the cached 1 after 1", got, loads)
	}
	// expired at its expiry
	if got := get(t0.Add(time.Minute), 2, t0.Add(2*time.Minute)); got != 2 || loads != 2 {
		t.Fatalf("get at the expiry: %d after %d loads, want 2 after 2", got, loads)
	}
	// an expiry already past caches nothing
	if got := get(t0.Add(3*time.Minute), 3, t0.Add(3*time.Minute)); got != 3 || loads != 3 {
		t.Fatalf("get after the expiry: %d after %d loads, want 3 after 3", got, loads)
	}
	if got := get(t0.Add(3*time.Minute), 4, t0.Add(4*time.Minute)); got != 4 || loads != 4 {
		t.Fatalf("get of a value loaded expired: %d after %d loads, want 4 after 4", got, loads)
	}

	// keys expire on their own
	if got, err := c.get(ctx, t0.Add(3*time.Minute), "other", load(5, t0.Add(5*time.Minute))); err != nil || got != 5 {
		t.Fatalf("get of another key: %d, %v", got, err)
	}
	if got := get(t0.Add(4*time.Minute), 6, t0.Add(6*time.Minute)); got != 6 {
		t.Fatalf("get of an expired key: %d, want 6", got)
	}
	if got, err := c.get(ctx, t0.Add(4*time.Minute), "other", load(7, t0.Add(7*time.Minute))); err != nil || got != 5 {
		t.Fatalf("get of another key, not expired: %d, %v, want the cached 5", got, err)
	}
}

func TestExpiringCacheError(t *testing.T) {
	var c expiringCache[string, []int]
	ctx := context.Background()
	t0 := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)

	// the error is returned as load returned it, and not cached
	_, err := c.get(ctx, t0, "k", func() ([]int, time.Time, error) {
		return nil, t0.Add(time.Minute), pgerr.ErrNotFound
	})
	if err != pgerr.ErrNotFound {
		t.Fatalf("get: %v, want pgerr.ErrNotFound itself", err)
	}
	got, err := c.get(ctx, t0, "k", func() ([]int, time.Time, error) {
		return []int{1}, t0.Add(time.Minute), nil
	})
	if err != nil || len(got) != 1 {
		t.Fatalf("get after an error: %v, %v, want a load", got, err)
	}

	// an empty value is a value
	if _, err := c.get(ctx, t0, "empty", func() ([]int, time.Time, error) { return nil, t0.Add(time.Minute), nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := c.get(ctx, t0, "empty", func() ([]int, time.Time, error) {
		t.Fatal("an empty value was loaded again")
		return nil, time.Time{}, nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestExpiringCacheLoadsOnce checks that the gets of a key being loaded wait for that load (run it with -race).
func TestExpiringCacheLoadsOnce(t *testing.T) {
	var c expiringCache[string, int]
	ctx := context.Background()
	t0 := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	var loads atomic.Int32
	release := make(chan struct{})
	load := func() (int, time.Time, error) {
		loads.Add(1)
		<-release
		return 1, t0.Add(time.Minute), nil
	}

	const gets = 8
	results := make(chan int, gets)
	var wg sync.WaitGroup
	for i := 0; i < gets; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := c.get(ctx, t0, "k", load)
			if err != nil {
				t.Error(err)
			}
			results <- v
		}()
	}
	// all but the loading get wait for the load
	for {
		c.mu.RLock()
		loading := len(c.loads) == 1
		c.mu.RUnlock()
		if loading {
			break
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)
	for v := range results {
		if v != 1 {
			t.Errorf("get: %d, want 1", v)
		}
	}
	if n := loads.Load(); n != 1 {
		t.Errorf("%d loads, want 1", n)
	}
}

func TestExpiringCacheWaitEndsWithContext(t *testing.T) {
	var c expiringCache[string, int]
	t0 := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	release := make(chan struct{})
	loading := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.get(context.Background(), t0, "k", func() (int, time.Time, error) {
			close(loading)
			<-release
			return 1, t0.Add(time.Minute), nil
		})
	}()
	<-loading

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.get(ctx, t0, "k", func() (int, time.Time, error) {
		t.Error("a key being loaded was loaded again")
		return 0, time.Time{}, nil
	}); !errors.Is(err, context.Canceled) {
		t.Errorf("get with an ended context: %v, want context.Canceled", err)
	}
	close(release)
	<-done
}

func TestExpiringCachePanic(t *testing.T) {
	var c expiringCache[string, int]
	ctx := context.Background()
	t0 := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic of load was not propagated")
			}
		}()
		_, _ = c.get(ctx, t0, "k", func() (int, time.Time, error) { panic("load") })
	}()
	got, err := c.get(ctx, t0, "k", func() (int, time.Time, error) { return 1, t0.Add(time.Minute), nil })
	if err != nil || got != 1 {
		t.Fatalf("get after a panicked load: %d, %v, want a load", got, err)
	}
}

func TestExpiringCacheIsBounded(t *testing.T) {
	var c expiringCache[string, int]
	ctx := context.Background()
	t0 := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	for i := 0; i <= expiringCacheMaxEntries; i++ {
		if _, err := c.get(ctx, t0, fmt.Sprint(i), func() (int, time.Time, error) { return i, t0.Add(time.Minute), nil }); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(c.entries); n > expiringCacheMaxEntries {
		t.Fatalf("the cache holds %d entries, more than %d", n, expiringCacheMaxEntries)
	}
}
