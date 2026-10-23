package cache

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
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

type testValue struct {
	ID   int
	Name string
	Tags []string
}

func TestGetCopiesTheCachedValue(t *testing.T) {
	c := NewSet[testValue]("test")
	tags := []string{"a"}
	c.Set("key", testValue{ID: 1, Name: "one", Tags: tags}, time.Minute)

	var v testValue
	if err := c.Get("key", &v); err != nil {
		t.Fatal(err)
	}
	if v.ID != 1 || v.Name != "one" || len(v.Tags) != 1 || &v.Tags[0] != &tags[0] {
		t.Errorf("got %+v, want the cached value (sharing its slice)", v)
	}
	if err := c.Get("missing", &v); err != ErrNotFound {
		t.Errorf("got %v for a missing key, want ErrNotFound", err)
	}
}

func TestGetKeepsNilSlicesAndMaps(t *testing.T) {
	slices := NewSet[[]int]("test")
	slices.Set("key", nil, time.Minute)
	s := []int{1}
	if err := slices.Get("key", &s); err != nil || s != nil {
		t.Errorf("got %v (%v), want a nil slice", s, err)
	}

	maps := NewSet[map[int]int]("test")
	maps.Set("key", nil, time.Minute)
	m := map[int]int{1: 1}
	if err := maps.Get("key", &m); err != nil || m != nil {
		t.Errorf("got %v (%v), want a nil map", m, err)
	}
}

func TestGetOfAPointerTypeKeepsThePointer(t *testing.T) {
	c := NewSet[*testValue]("test")
	cached := &testValue{ID: 1}
	c.Set("key", cached, time.Minute)

	var v *testValue
	if err := c.Get("key", &v); err != nil || v != cached {
		t.Errorf("got %p (%v), want the cached pointer %p", v, err, cached)
	}

	calculated := &testValue{ID: 2}
	if _, err := c.MutexGetSet("other", &v, func() (**testValue, error) { return &calculated, nil }, time.Minute); err != nil || v != calculated {
		t.Errorf("got %p (%v), want the calculated pointer %p", v, err, calculated)
	}
}

func BenchmarkSetGet(b *testing.B) {
	// keep the trace log of Set out of the benchmark output
	level := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	defer zerolog.SetGlobalLevel(level)

	c := NewSet[testValue]("test")
	c.Set("key", testValue{ID: 1, Name: "one", Tags: []string{"a"}}, time.Minute)
	var v testValue
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := c.Get("key", &v); err != nil {
			b.Fatal(err)
		}
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

// refreshBlocked starts a Refresh of key whose calculation returns calculated once released, and waits until it
// calculates. cached receives whether the refresh cached it.
func refreshBlocked(c *Set[int], key string, calculated int) (release func(), cached <-chan bool) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	results := make(chan bool, 1)
	go func() {
		ok, err := c.Refresh(key, func() (*int, error) {
			close(started)
			<-unblock
			return &calculated, nil
		}, time.Minute, nil)
		if err != nil {
			panic(err)
		}
		results <- ok
	}()
	<-started
	return func() { close(unblock) }, results
}

func TestRefresh(t *testing.T) {
	c := NewSet[int]("test")
	c.Set("key", 1, time.Minute)
	release, cached := refreshBlocked(c, "key", 2)
	var v int
	if err := c.Get("key", &v); err != nil || v != 1 {
		t.Errorf("got %d (%v) during the refresh, want the previous 1", v, err)
	}
	release()
	if !<-cached {
		t.Error("the refresh did not cache its value")
	}
	if err := c.Get("key", &v); err != nil || v != 2 {
		t.Errorf("got %d (%v) after the refresh, want 2", v, err)
	}
	if len(c.calculating) != 0 {
		t.Errorf("calculating %v after the refresh, want nothing", c.calculating)
	}
}

func TestRefreshDoesNotCacheAfterAWrite(t *testing.T) {
	tests := []struct {
		name  string
		write func(c *Set[int])
		want  int // 0: not cached
	}{
		// e.g. purged after a recalculation of the data the refresh may have read before
		{"delete", func(c *Set[int]) { _ = c.Delete("key") }, 0},
		{"flush", func(c *Set[int]) { _ = c.Flush() }, 0},
		// a value calculated from newer data
		{"set", func(c *Set[int]) { c.Set("key", 3, time.Minute) }, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewSet[int]("test")
			c.Set("key", 1, time.Minute)
			release, cached := refreshBlocked(c, "key", 2)
			tt.write(c)
			release()
			if <-cached {
				t.Error("the refresh cached its value after a write")
			}
			var v int
			err := c.Get("key", &v)
			if tt.want == 0 && err == nil {
				t.Errorf("cached %d, want nothing", v)
			}
			if tt.want != 0 && (err != nil || v != tt.want) {
				t.Errorf("cached %d (%v), want %d", v, err, tt.want)
			}
		})
	}
}

func TestRefreshAndMutexGetSetCalculateOneAfterTheOther(t *testing.T) {
	c := NewSet[int]("test")
	release, cached := refreshBlocked(c, "key", 2)
	var calculated atomic.Bool
	result := make(chan int, 1)
	go func() {
		var v int
		if _, err := c.MutexGetSet("key", &v, func() (*int, error) {
			calculated.Store(true)
			one := 1
			return &one, nil
		}, time.Minute); err != nil {
			t.Error(err)
		}
		result <- v
	}()
	time.Sleep(50 * time.Millisecond)
	if calculated.Load() {
		t.Fatal("a request calculated the key during its refresh")
	}
	release()
	if !<-cached {
		t.Error("the refresh did not cache its value")
	}
	if v := <-result; v != 2 || calculated.Load() {
		t.Errorf("the request got %d (calculated: %v), want the refreshed 2", v, calculated.Load())
	}
}

func TestRefreshPublishing(t *testing.T) {
	c := NewSet[int]("test")
	var published []bool
	publish := func(value *int, cacheValue func() bool) { published = append(published, cacheValue()) }
	two := 2
	if ok, err := c.Refresh("key", func() (*int, error) { return &two, nil }, time.Minute, publish); err != nil || !ok {
		t.Fatalf("Refresh: %v, %v", ok, err)
	}
	if len(published) != 1 || !published[0] {
		t.Errorf("publish was told %v, want that the value is cached", published)
	}
}

func TestRefreshFailure(t *testing.T) {
	c := NewSet[int]("test")
	c.Set("key", 1, time.Minute)
	if ok, err := c.Refresh("key", func() (*int, error) { return nil, ErrNotFound }, time.Minute, nil); err == nil || ok {
		t.Errorf("Refresh: %v, %v; want the error", ok, err)
	}
	func() {
		defer func() { _ = recover() }()
		_, _ = c.Refresh("key", func() (*int, error) { panic("refresh") }, time.Minute, nil)
	}()
	var v int
	if err := c.Get("key", &v); err != nil || v != 1 {
		t.Errorf("got %d (%v), want the previous 1: dropping it is up to the caller", v, err)
	}
	if len(c.calculating) != 0 {
		t.Errorf("calculating %v after failed refreshes, want nothing", c.calculating)
	}
}

func TestUsed(t *testing.T) {
	c := NewSet[int]("test")
	if c.Used("key") {
		t.Error("a key never cached is used")
	}

	// written only, e.g. by the worker's refreshes: not used until read
	c.Set("key", 1, time.Minute)
	if c.Used("key") || !c.Has("key") {
		t.Errorf("Used %v, Has %v after a Set, want only cached", c.Used("key"), c.Has("key"))
	}
	var v int
	if err := c.Get("key", &v); err != nil {
		t.Fatal(err)
	}
	if !c.Used("key") {
		t.Error("a key read is not used")
	}
	c.Set("key", 2, time.Minute)
	if c.Used("key") {
		t.Error("a key written after it was read is used")
	}
	if _, err := c.MutexGetSet("key", &v, nil, time.Minute); err != nil || !c.Used("key") {
		t.Errorf("a key MutexGetSet read is not used (%v)", err)
	}
	if _, err := c.Refresh("key", func() (*int, error) { three := 3; return &three, nil }, time.Minute, nil); err != nil {
		t.Fatal(err)
	}
	if c.Used("key") {
		t.Error("a key refreshed is used")
	}

	// calculated for a request: used, also while it is calculated
	release, result := calculateBlocked(c, "requested", 4)
	if !c.Used("requested") || c.Has("requested") {
		t.Errorf("Used %v, Has %v while MutexGetSet calculates the key, want used only", c.Used("requested"), c.Has("requested"))
	}
	release()
	<-result
	if !c.Used("requested") {
		t.Error("a key MutexGetSet calculated is not used")
	}

	// a calculation that failed or panicked leaves nothing used
	if _, err := c.MutexGetSet("failed", &v, func() (*int, error) { return nil, ErrNotFound }, time.Minute); err == nil {
		t.Error("MutexGetSet succeeded without a value")
	}
	func() {
		defer func() { _ = recover() }()
		_, _ = c.MutexGetSet("panicked", &v, func() (*int, error) { panic("calculation") }, time.Minute)
	}()
	if c.Used("failed") || c.Used("panicked") {
		t.Error("a key whose calculation failed is used")
	}
	if len(c.calculating) != 0 || len(c.requested) != 0 {
		t.Errorf("calculating %v, requested %v after the calculations, want nothing", c.calculating, c.requested)
	}
}
