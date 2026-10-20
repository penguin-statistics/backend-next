package calcwkr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"

	"exusiai.dev/backend-next/internal/pkg/observability"
)

// newTestRedSync connects to PENGUIN_TEST_REDIS_URL, a disposable Redis, e.g.
//
//	docker run -d --rm -p 127.0.0.1:56379:6379 redis:6.2.7-alpine
//	PENGUIN_TEST_REDIS_URL=redis://127.0.0.1:56379/0 go test ./internal/workers/calcwkr/
func newTestRedSync(t *testing.T) *redsync.Redsync {
	t.Helper()
	url := os.Getenv("PENGUIN_TEST_REDIS_URL")
	if url == "" {
		t.Skip("PENGUIN_TEST_REDIS_URL is not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { client.Close() })
	return redsync.New(goredis.NewPool(client))
}

func newTestWorker(t *testing.T, rs *redsync.Redsync, expiry time.Duration) *Worker {
	t.Helper()
	name := fmt.Sprintf("mutex:calcwkr:test:%d", time.Now().UnixNano())
	return &Worker{syncMutex: rs.NewMutex(name, redsync.WithExpiry(expiry), redsync.WithTries(1))}
}

func TestMicrotasksOfConcurrentServersKeepTheLock(t *testing.T) {
	rs := newTestRedSync(t)
	w := newTestWorker(t, rs, 5*time.Second)
	if err := w.lock(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// the microtasks of every server renew the lock at the same time, which the race detector checks
	var wg sync.WaitGroup
	for _, server := range constant.Servers {
		server := server
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := w.microtask(ctx, "lockTest", server, func(context.Context) error {
					time.Sleep(600 * time.Millisecond)
					return nil
				}); err != nil {
					t.Error(err)
				}
			}()
		}
	}
	wg.Wait()

	// another worker cannot take the lock while it is held
	other := rs.NewMutex(w.syncMutex.Name(), redsync.WithExpiry(5*time.Second), redsync.WithTries(1))
	if err := other.Lock(); err == nil {
		t.Fatal("another worker acquired the lock held by the worker")
	}
	cancel()
	ok, err := w.syncMutex.Unlock()
	if err != nil || !ok {
		t.Fatalf("the worker no longer held its lock: %v %v", ok, err)
	}
	if err := other.Lock(); err != nil {
		t.Fatalf("the lock was not released: %v", err)
	}
	other.Unlock()
}

func TestMicrotaskReturnsTheErrorAndRecordsTheDuration(t *testing.T) {
	rs := newTestRedSync(t)
	w := newTestWorker(t, rs, 10*time.Second)
	if err := w.lock(); err != nil {
		t.Fatal(err)
	}
	defer w.unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errFailed := errors.New("failed")
	if err := w.microtask(ctx, "microtaskTest", "CN", func(context.Context) error {
		time.Sleep(20 * time.Millisecond)
		return errFailed
	}); !errors.Is(err, errFailed) {
		t.Fatalf("got %v, want %v", err, errFailed)
	}
	if d := testutil.ToFloat64(observability.WorkerCalcDuration.WithLabelValues("microtaskTest", "CN")); d < 0.02 {
		t.Fatalf("recorded duration %fs, want at least 0.02s", d)
	}
}

func TestCheckConfigRejectsNegativeDurations(t *testing.T) {
	for i, w := range []*Worker{{sep: -1}, {interval: -1}, {timeout: -1}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("no panic for config %d", i)
				}
			}()
			w.checkConfig()
		}()
	}
	(&Worker{}).checkConfig()
}
