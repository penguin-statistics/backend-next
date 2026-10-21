package calcwkr

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-redsync/redsync/v4"
	redsyncredis "github.com/go-redsync/redsync/v4/redis"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"exusiai.dev/backend-next/internal/pkg/observability"
)

// busyPool is a redsync pool whose mutexes another worker holds.
type busyPool struct{}

func (busyPool) Get(context.Context) (redsyncredis.Conn, error) { return busyConn{}, nil }

type busyConn struct{ lockedConn }

func (busyConn) SetNX(string, string, time.Duration) (bool, error) { return false, nil }

// histogram returns the observations of the histogram o so far.
func histogram(t *testing.T, o prometheus.Observer) *dto.Histogram {
	t.Helper()
	var m dto.Metric
	if err := o.(prometheus.Metric).Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram()
}

// sampleCount returns how many observations the histogram o has.
func sampleCount(t *testing.T, o prometheus.Observer) uint64 {
	t.Helper()
	return histogram(t, o).GetSampleCount()
}

// counts returns the value of the counter of every outcome in outcomes; labels come before the outcome.
func counts(c *prometheus.CounterVec, labels []string, outcomes []string) map[string]float64 {
	m := make(map[string]float64, len(outcomes))
	for _, outcome := range outcomes {
		m[outcome] = testutil.ToFloat64(c.WithLabelValues(append(labels[:len(labels):len(labels)], outcome)...))
	}
	return m
}

// assertCountedOnce fails unless, of the counters before and after, only the one of outcome went up, by 1.
func assertCountedOnce(t *testing.T, before, after map[string]float64, outcome string) {
	t.Helper()
	for o := range before {
		want := 0.0
		if o == outcome {
			want = 1
		}
		if got := after[o] - before[o]; got != want {
			t.Errorf("%s: counted %v %s, want %v", outcome, got, o, want)
		}
	}
}

func TestBatchRecordsItsOutcomeAndDuration(t *testing.T) {
	previous := lockRetryDelay
	lockRetryDelay = 0
	t.Cleanup(func() { lockRetryDelay = previous })

	errFailed := errors.New("failed")
	// the servers of the timed out batch run on until the test ends
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	for _, tc := range []struct {
		outcome   string
		pool      redsyncredis.Pool
		f         func(ctx context.Context, server string) error
		interval  time.Duration
		durations uint64
	}{
		// the interval the batch waits after it ends is not part of its duration
		{"ok", lockedPool{}, func(context.Context, string) error { return nil }, 200 * time.Millisecond, 1},
		{"error", lockedPool{}, func(context.Context, string) error { return errFailed }, 0, 1},
		{"timeout", lockedPool{}, func(context.Context, string) error { <-release; return nil }, 0, 1},
		{"lock_not_acquired", busyPool{}, func(context.Context, string) error {
			t.Error("ran a batch without the lock")
			return nil
		}, 0, 0},
	} {
		w := &Worker{
			timeout:   100 * time.Millisecond,
			interval:  tc.interval,
			syncMutex: redsync.New(tc.pool).NewMutex("mutex:calcwkr:test", redsync.WithTries(1)),
		}
		outcomes := []string{"ok", "error", "timeout", "lock_not_acquired"}
		batches := counts(observability.WorkerCalcBatches, nil, outcomes)
		durations := histogram(t, observability.WorkerCalcBatchDuration)

		w.batch(context.Background(), WorkerCalcTypeStatsCalc, tc.f)

		assertCountedOnce(t, batches, counts(observability.WorkerCalcBatches, nil, outcomes), tc.outcome)
		after := histogram(t, observability.WorkerCalcBatchDuration)
		if got := after.GetSampleCount() - durations.GetSampleCount(); got != tc.durations {
			t.Errorf("%s: observed %d batch durations, want %d", tc.outcome, got, tc.durations)
		}
		if tc.interval > 0 {
			if got := after.GetSampleSum() - durations.GetSampleSum(); got >= tc.interval.Seconds() {
				t.Errorf("%s: observed a batch duration of %vs, which includes the interval of %v", tc.outcome, got, tc.interval)
			}
		}
	}
}

func TestMicrotaskRecordsThePhaseDuration(t *testing.T) {
	w := &Worker{syncMutex: redsync.New(lockedPool{}).NewMutex("mutex:calcwkr:test")}
	errFailed := errors.New("failed")
	// a phase that fails still has its duration recorded
	for _, tc := range []struct {
		phase, server string
		err           error
	}{
		{"patternMatrix", "JP", nil},
		{"siteStats", "KR", errFailed},
	} {
		phase := observability.WorkerCalcPhaseDuration.WithLabelValues(tc.phase, tc.server)
		before := sampleCount(t, phase)

		if err := w.microtask(context.Background(), tc.phase, tc.server, func(context.Context) error { return tc.err }); err != tc.err {
			t.Fatalf("%s: got error %v, want %v", tc.phase, err, tc.err)
		}
		if got := sampleCount(t, phase) - before; got != 1 {
			t.Fatalf("%s: observed %d phase durations, want 1", tc.phase, got)
		}
	}
}

func TestGlobalMatrixRefreshRecordsItsOutcomeAndDuration(t *testing.T) {
	w := &Worker{timeout: time.Minute, refreshGlobalMatrices: true}
	const name, server = "globalDropMatrixRefresh", "KR"
	for _, tc := range []struct {
		outcome string
		refresh func(context.Context, string) error
	}{
		{"ok", func(context.Context, string) error { return nil }},
		{"error", func(context.Context, string) error { return errors.New("failed") }},
		{"panic", func(context.Context, string) error { panic("failed") }},
	} {
		outcomes := []string{"ok", "error", "panic"}
		refreshes := observability.WorkerRefreshes.WithLabelValues(name, server, tc.outcome)
		before := counts(observability.WorkerRefreshes, []string{name, server}, outcomes)
		durations := sampleCount(t, observability.WorkerRefreshDuration.WithLabelValues(name, server))

		w.globalMatrixRefresh(context.Background(), name, server, tc.refresh)

		// the refresh runs in the background
		for deadline := time.Now().Add(5 * time.Second); testutil.ToFloat64(refreshes) == before[tc.outcome]; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%s: the refresh was not counted", tc.outcome)
			}
		}
		assertCountedOnce(t, before, counts(observability.WorkerRefreshes, []string{name, server}, outcomes), tc.outcome)
		if got := sampleCount(t, observability.WorkerRefreshDuration.WithLabelValues(name, server)) - durations; got != 1 {
			t.Errorf("%s: observed %d refresh durations, want 1", tc.outcome, got)
		}
	}
}
