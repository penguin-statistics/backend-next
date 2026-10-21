package cache

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"exusiai.dev/backend-next/internal/pkg/observability"
)

// counts are the metrics of a cache. The metrics are global, so tests compare the counts since they started, which
// keeps them passing when run more than once in a process.
type counts struct {
	hits, misses, calculations, sets, deletes, flushes int
}

func readCounts(t *testing.T, name string) counts {
	t.Helper()
	var m dto.Metric
	if err := observability.CacheCalcDuration.WithLabelValues(name).(prometheus.Metric).Write(&m); err != nil {
		t.Fatal(err)
	}
	count := func(c prometheus.Collector) int { return int(testutil.ToFloat64(c)) }
	return counts{
		hits:         count(observability.CacheLookups.WithLabelValues(name, "hit")),
		misses:       count(observability.CacheLookups.WithLabelValues(name, "miss")),
		calculations: int(m.GetHistogram().GetSampleCount()),
		sets:         count(observability.CacheWrites.WithLabelValues(name, "set")),
		deletes:      count(observability.CacheWrites.WithLabelValues(name, "delete")),
		flushes:      count(observability.CacheWrites.WithLabelValues(name, "flush")),
	}
}

// countsSince returns a function that returns the counts of the cache name since countsSince was called
func countsSince(t *testing.T, name string) func() counts {
	t.Helper()
	start := readCounts(t, name)
	return func() counts {
		t.Helper()
		now := readCounts(t, name)
		return counts{
			hits:         now.hits - start.hits,
			misses:       now.misses - start.misses,
			calculations: now.calculations - start.calculations,
			sets:         now.sets - start.sets,
			deletes:      now.deletes - start.deletes,
			flushes:      now.flushes - start.flushes,
		}
	}
}

func TestSetMetrics(t *testing.T) {
	const name = "metricsTestSet#key"
	c := NewSet[int](name)
	counted := countsSince(t, name)
	var v int
	_ = c.Get("a", &v)
	c.Set("a", 1, time.Minute)
	_ = c.Get("a", &v)
	if got, want := counted(), (counts{hits: 1, misses: 1, sets: 1}); got != want {
		t.Errorf("got %+v after Get, want %+v", got, want)
	}

	valueFunc := func() (*int, error) {
		two := 2
		return &two, nil
	}
	for i := 0; i < 2; i++ {
		if _, err := c.MutexGetSet("b", &v, valueFunc, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.MutexGetSet("c", &v, func() (*int, error) { return nil, errors.New("failed") }, time.Minute); err == nil {
		t.Fatal("got no error from a failed calculation")
	}
	// MutexGetSet is no Set call
	if got, want := counted(), (counts{hits: 2, misses: 3, calculations: 2, sets: 1}); got != want {
		t.Errorf("got %+v after MutexGetSet, want %+v", got, want)
	}

	_ = c.Delete("a")
	_ = c.Flush()
	if got, want := counted(), (counts{hits: 2, misses: 3, calculations: 2, sets: 1, deletes: 1, flushes: 1}); got != want {
		t.Errorf("got %+v after Delete and Flush, want %+v", got, want)
	}
}

func TestSetMetricsOfAWaitingMutexGetSet(t *testing.T) {
	const name = "metricsTestSetWaiting"
	c := NewSet[int](name)
	counted := countsSince(t, name)
	release, result := calculateBlocked(c, "key", 1)

	waited := make(chan int)
	go func() {
		var v int
		if _, err := c.MutexGetSet("key", &v, func() (*int, error) {
			t.Error("calculated a key another MutexGetSet calculated")
			return &v, nil
		}, time.Minute); err != nil {
			t.Error(err)
		}
		waited <- v
	}()
	// wait until the second MutexGetSet waits for the key's lock
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		c.locksMu.Lock()
		users := c.locks["key"].users
		c.locksMu.Unlock()
		if users == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the second MutexGetSet did not wait for the key's lock")
		}
	}
	release()
	<-result
	if v := <-waited; v != 1 {
		t.Errorf("the waiting MutexGetSet got %d, want 1", v)
	}

	// the waiting MutexGetSet found the value on its second lookup, and counts a hit once
	if got, want := counted(), (counts{hits: 1, misses: 1, calculations: 1}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSetMetricsOfAnUnpublishedCalculation(t *testing.T) {
	const name = "metricsTestSetUnpublished"
	c := NewSet[int](name)
	counted := countsSince(t, name)
	release, result := calculateBlocked(c, "key", 1)
	c.Set("key", 2, time.Minute)
	release()
	if v := <-result; v != 2 {
		t.Errorf("got %d, want the newer 2", v)
	}

	// reading the newer value instead of the calculated one is no further lookup
	if got, want := counted(), (counts{misses: 1, calculations: 1, sets: 1}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSingularMetrics(t *testing.T) {
	const name = "metricsTestSingular"
	c := NewSingular[int](name)
	counted := countsSince(t, name)
	var v int
	_ = c.Get(&v)
	c.Set(1, time.Minute)
	_ = c.Get(&v)
	if got, want := counted(), (counts{hits: 1, misses: 1, sets: 1}); got != want {
		t.Errorf("got %+v after Get, want %+v", got, want)
	}

	_ = c.Delete()
	for i := 0; i < 2; i++ {
		if err := c.MutexGetSet(&v, func() (int, error) { return 2, nil }, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	// MutexGetSet is no Set call
	if got, want := counted(), (counts{hits: 2, misses: 2, calculations: 1, sets: 1, deletes: 1}); got != want {
		t.Errorf("got %+v after MutexGetSet, want %+v", got, want)
	}

	// a MutexGetSet that waited for the mutex while another calculated finds the value on its second lookup. Which
	// MutexGetSet waits for the mutex cannot be observed, so the second lookup is called directly.
	if err := c.slowMutexGetSet(&v, func() (int, error) {
		t.Error("calculated a value in the cache")
		return 3, nil
	}, time.Minute); err != nil || v != 2 {
		t.Errorf("got %d, %v, want 2", v, err)
	}
	if got, want := counted(), (counts{hits: 3, misses: 2, calculations: 1, sets: 1, deletes: 1}); got != want {
		t.Errorf("got %+v after the second lookup, want %+v", got, want)
	}
}
