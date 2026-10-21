package cache

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMutexGetSetDoesNotMakeOtherKeysWait(t *testing.T) {
	c := NewSet[int]("test")
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		var v int
		_, _ = c.MutexGetSet("slow", &v, func() (*int, error) {
			close(started)
			<-release
			one := 1
			return &one, nil
		}, time.Minute)
	}()
	<-started

	done := make(chan int)
	go func() {
		var v int
		if _, err := c.MutexGetSet("fast", &v, func() (*int, error) {
			two := 2
			return &two, nil
		}, time.Minute); err != nil {
			t.Error(err)
		}
		done <- v
	}()
	select {
	case v := <-done:
		if v != 2 {
			t.Errorf("got %d, want 2", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("calculating a key waited for the calculation of another key")
	}
	close(release)
}

func TestMutexGetSetCalculatesAKeyOnce(t *testing.T) {
	c := NewSet[int]("test")
	var calculations atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var v int
			if _, err := c.MutexGetSet("key", &v, func() (*int, error) {
				calculations.Add(1)
				time.Sleep(20 * time.Millisecond)
				three := 3
				return &three, nil
			}, time.Minute); err != nil || v != 3 {
				t.Errorf("got %d, %v", v, err)
			}
		}()
	}
	wg.Wait()
	if n := calculations.Load(); n != 1 {
		t.Errorf("the key was calculated %d times, want once", n)
	}
}

// calculateBlocked starts a MutexGetSet of key whose calculation returns calculated once released, and waits until
// it calculates.
func calculateBlocked(c *Set[int], key string, calculated int) (release func(), result <-chan int) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	results := make(chan int, 1)
	go func() {
		var v int
		_, _ = c.MutexGetSet(key, &v, func() (*int, error) {
			close(started)
			<-unblock
			return &calculated, nil
		}, time.Minute)
		results <- v
	}()
	<-started
	return func() { close(unblock) }, results
}

func TestMutexGetSetDoesNotOverwriteANewerValue(t *testing.T) {
	c := NewSet[int]("test")
	release, result := calculateBlocked(c, "key", 1) // calculating from data the refresh below supersedes
	c.Set("key", 2, time.Minute)                     // the worker's refresh
	release()

	if v := <-result; v != 2 {
		t.Errorf("the request got %d, want the newer 2", v)
	}
	var cached int
	if err := c.Get("key", &cached); err != nil || cached != 2 {
		t.Errorf("cached %d (%v), want the newer 2", cached, err)
	}
}

func TestMutexGetSetDoesNotCacheAfterADelete(t *testing.T) {
	c := NewSet[int]("test")
	release, result := calculateBlocked(c, "key", 1)
	if err := c.Delete("key"); err != nil {
		t.Fatal(err)
	}
	release()

	if v := <-result; v != 1 {
		t.Errorf("the request got %d, want its calculation 1", v)
	}
	var cached int
	if err := c.Get("key", &cached); err == nil {
		t.Errorf("cached %d calculated before the delete", cached)
	}
}

// the value MutexGetSetPublishing returns is the one cacheValue reports, even if the key is deleted right after
func TestMutexGetSetPublishingDeletedAfterPublication(t *testing.T) {
	c := NewSet[int]("test")
	var (
		v        int
		returned bool
	)
	if _, err := c.MutexGetSetPublishing("key", &v, func() (*int, error) {
		c.Set("key", 2, time.Minute) // the worker's refresh
		one := 1
		return &one, nil
	}, time.Minute, func(value *int, cacheValue func() bool) {
		returned = cacheValue()
		_ = c.Delete("key") // a failed refresh
	}); err != nil {
		t.Fatal(err)
	}
	if v != 2 || returned {
		t.Errorf("got %d, cacheValue reported %v; want the refreshed 2, reported as not the calculation", v, returned)
	}
}

func TestMutexGetSetForgetsKeysNotInUse(t *testing.T) {
	c := NewSet[int]("test")
	for i := 0; i < 100; i++ {
		var v int
		if _, err := c.MutexGetSet("key"+strconv.Itoa(i), &v, func() (*int, error) { return &i, nil }, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.locks) != 0 || len(c.calculating) != 0 {
		t.Errorf("kept %d locks and %d calculating keys", len(c.locks), len(c.calculating))
	}
}

// MutexGetSetPublishing caches a calculated value through publish, which learns whether it is the value returned
func TestMutexGetSetPublishing(t *testing.T) {
	tests := []struct {
		name string
		// meanwhile happens during the calculation of 1
		meanwhile    func(c *Set[int])
		want         int
		wantReturned bool
		wantCached   bool
	}{
		{"published", func(c *Set[int]) {}, 1, true, true},
		// the worker's refresh is returned instead
		{"written meanwhile", func(c *Set[int]) { c.Set("key", 2, time.Minute) }, 2, false, true},
		// the calculation is returned, but not cached
		{"deleted meanwhile", func(c *Set[int]) { _ = c.Delete("key") }, 1, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewSet[int]("test")
			var (
				v         int
				published []int
				returned  bool
			)
			calculated, err := c.MutexGetSetPublishing("key", &v, func() (*int, error) {
				tt.meanwhile(c)
				one := 1
				return &one, nil
			}, time.Minute, func(value *int, cacheValue func() bool) {
				published = append(published, *value)
				returned = cacheValue()
			})
			if err != nil || !calculated {
				t.Fatalf("calculated %v, %v", calculated, err)
			}
			if v != tt.want || returned != tt.wantReturned {
				t.Errorf("got %d, cacheValue reported %v; want %d, %v", v, returned, tt.want, tt.wantReturned)
			}
			if len(published) != 1 || published[0] != 1 {
				t.Errorf("published %v, want the calculation [1]", published)
			}
			var cached int
			if err := c.Get("key", &cached); (err == nil) != tt.wantCached || (tt.wantCached && cached != tt.want) {
				t.Errorf("cached %d (%v)", cached, err)
			}
		})
	}
}
