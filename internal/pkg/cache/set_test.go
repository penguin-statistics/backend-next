package cache

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSetMutexGetSetDifferentKeysRunInParallel(t *testing.T) {
	s := NewSet[int]("test")

	const keys = 4
	started := make(chan struct{}, keys)
	release := make(chan struct{})

	var wg sync.WaitGroup
	for i := 0; i < keys; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var dest int
			_, err := s.MutexGetSet(string(rune('a'+i)), &dest, func() (*int, error) {
				started <- struct{}{}
				<-release
				v := i
				return &v, nil
			}, time.Minute)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if dest != i {
				t.Errorf("dest = %d, want %d", dest, i)
			}
		}(i)
	}

	// every valueFunc must start before any of them is released
	for i := 0; i < keys; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of %d valueFuncs started; different keys are serialized", i, keys)
		}
	}
	close(release)
	wg.Wait()
}

func TestSetMutexGetSetSameKeySharesOneCall(t *testing.T) {
	s := NewSet[int]("test")

	var calls atomic.Int32
	release := make(chan struct{})

	const callers = 8
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var dest int
			_, err := s.MutexGetSet("k", &dest, func() (*int, error) {
				calls.Add(1)
				<-release
				v := 42
				return &v, nil
			}, time.Minute)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if dest != 42 {
				t.Errorf("dest = %d, want 42", dest)
			}
		}()
	}

	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()

	if n := calls.Load(); n != 1 {
		t.Fatalf("valueFunc called %d times, want 1", n)
	}

	// later calls read from the cache
	var dest int
	calculated, err := s.MutexGetSet("k", &dest, func() (*int, error) {
		t.Fatal("valueFunc must not run for a cached key")
		return nil, nil
	}, time.Minute)
	if err != nil || calculated || dest != 42 {
		t.Fatalf("got calculated=%v dest=%d err=%v, want false 42 nil", calculated, dest, err)
	}
}
