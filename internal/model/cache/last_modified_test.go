package cache

import (
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/goccy/go-json"

	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/cache"
)

func TestSetLastModifiedTime(t *testing.T) {
	Initialize()
	const key = "[test#TestSetLastModifiedTime]"
	t.Cleanup(func() {
		forgetLastModifiedTime(key)
	})
	lastModified := func() time.Time {
		t.Helper()
		var lastModified time.Time
		if err := LastModifiedTime.Get(key, &lastModified); err != nil {
			t.Fatal(err)
		}
		return lastModified
	}
	// what services cache: recalculated values are new instances
	stages := func(code string) []*modelv2.Stage {
		return []*modelv2.Stage{{ArkStageID: "main_01-07", Code: code}}
	}
	// what Last-Modified tells apart
	second := func(t time.Time) time.Time {
		return t.Truncate(time.Second)
	}

	SetLastModifiedTime(key, stages("1-7"), func() {})
	first := lastModified()

	SetLastModifiedTime(key, stages("1-7"), func() {})
	if got := lastModified(); !got.Equal(first) {
		t.Errorf("an unchanged value changed the last modified time from %v to %v", first, got)
	}

	// changes, even in the same second, are recorded in later seconds
	SetLastModifiedTime(key, stages("1-8"), func() {})
	changed := lastModified()
	if !second(changed).After(second(first)) {
		t.Errorf("a changed value recorded the last modified time %v, not in a second after %v", changed, first)
	}
	SetLastModifiedTime(key, stages("1-7"), func() {})
	changedBack := lastModified()
	if !second(changedBack).After(second(changed)) {
		t.Errorf("a changed value recorded the last modified time %v, not in a second after %v", changedBack, changed)
	}

	// a flushed last modified time of an unchanged value is recorded again as it was
	if err := LastModifiedTime.Flush(); err != nil {
		t.Fatal(err)
	}
	SetLastModifiedTime(key, stages("1-7"), func() {})
	if got := lastModified(); !got.Equal(changedBack) {
		t.Errorf("an unchanged value recorded the flushed last modified time %v as %v", changedBack, got)
	}
}

// forgetLastModifiedTime deletes the last modified time of key and its record, so that tests run again (-count) start
// without them.
func forgetLastModifiedTime(key string) {
	_ = LastModifiedTime.Delete(key)
	lastModifiedMu.Lock()
	defer lastModifiedMu.Unlock()
	delete(lastModifiedRecords, key)
}

// of two recalculations of different values, the value cached last must be the one recorded last, whatever the order
// in which they get to record
func TestSetLastModifiedTimeConcurrently(t *testing.T) {
	Initialize()
	const key = "[test#TestSetLastModifiedTimeConcurrently]"
	t.Cleanup(func() {
		forgetLastModifiedTime(key)
	})
	var (
		cachedMu sync.Mutex
		cached   string
	)
	set := func(value string) {
		cachedMu.Lock()
		defer cachedMu.Unlock()
		cached = value
	}
	// the old value is cached, then the new one is recalculated before the old one is recorded
	done := make(chan struct{})
	SetLastModifiedTime(key, "old", func() {
		set("old")
		go func() {
			defer close(done)
			SetLastModifiedTime(key, "new", func() { set("new") })
		}()
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond): // the new one waits for the old one to be recorded
		}
	})
	<-done

	b, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	lastModifiedMu.Lock()
	defer lastModifiedMu.Unlock()
	if lastModifiedRecords[key].digest != sha256.Sum256(b) {
		t.Errorf("the value cached last, %q, is not the one recorded last", cached)
	}
}

// recordedDigest returns the digest of the value recorded for key, and whether there is one
func recordedDigest(key string) ([sha256.Size]byte, bool) {
	lastModifiedMu.Lock()
	defer lastModifiedMu.Unlock()
	record, ok := lastModifiedRecords[key]
	return record.digest, ok
}

func digestOf(t *testing.T, value any) [sha256.Size]byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

// a value calculated by MutexGetSet (on a request) and one refreshed meanwhile (by the worker) are recorded in the
// order they are cached: the value recorded last is the one cached
func TestPublishingLastModifiedTime(t *testing.T) {
	Initialize()
	const key = "[test#TestPublishingLastModifiedTime]"
	t.Cleanup(func() {
		forgetLastModifiedTime(key)
	})
	set := cache.NewSet[int]("test")
	publish := PublishingLastModifiedTime[int](key)
	refresh := func(v int) {
		SetLastModifiedTime(key, v, func() { set.Set("key", v, time.Minute) })
	}
	lastModified := func() time.Time {
		t.Helper()
		var lastModified time.Time
		if err := LastModifiedTime.Get(key, &lastModified); err != nil {
			t.Fatal(err)
		}
		return lastModified
	}

	// the request calculates 1 from older data while the worker refreshes it as 2
	var got int
	if _, err := set.MutexGetSetPublishing("key", &got, func() (*int, error) {
		refresh(2)
		one := 1
		return &one, nil
	}, time.Minute, publish); err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Errorf("the request got %d, want the refreshed 2", got)
	}
	if digest, _ := recordedDigest(key); digest != digestOf(t, 2) {
		t.Error("the value recorded is not the one cached, 2")
	}

	// the worker refreshes it back as 1: a change, recorded in a later second
	refreshed := lastModified()
	refresh(1)
	if got := lastModified(); !got.Truncate(time.Second).After(refreshed.Truncate(time.Second)) {
		t.Errorf("changing back to 1 recorded %v, not in a second after %v", got, refreshed)
	}

	// the cached value is deleted (by a failed refresh) during a calculation, which the request gets, uncached: it is
	// recorded, as it is sent
	if err := set.Delete("key"); err != nil {
		t.Fatal(err)
	}
	if _, err := set.MutexGetSetPublishing("key", &got, func() (*int, error) {
		_ = set.Delete("key")
		three := 3
		return &three, nil
	}, time.Minute, publish); err != nil {
		t.Fatal(err)
	}
	if digest, _ := recordedDigest(key); got != 3 || digest != digestOf(t, 3) {
		t.Errorf("the request got %d, and 3 is not the value recorded", got)
	}
}

// requests calculating the value while the worker refreshes it: whatever the order, the value recorded last is the one
// cached
func TestPublishingLastModifiedTimeConcurrently(t *testing.T) {
	Initialize()
	const key = "[test#TestPublishingLastModifiedTimeConcurrently]"
	t.Cleanup(func() {
		forgetLastModifiedTime(key)
	})
	set := cache.NewSet[int]("test")
	publish := PublishingLastModifiedTime[int](key)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		i := i
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = set.Delete("key") // expired
			var v int
			_, _ = set.MutexGetSetPublishing("key", &v, func() (*int, error) {
				calculated := i % 3
				return &calculated, nil
			}, time.Minute, publish)
		}()
		go func() {
			defer wg.Done()
			refreshed := i % 5
			SetLastModifiedTime(key, refreshed, func() { set.Set("key", refreshed, time.Minute) })
		}()
	}
	wg.Wait()

	var cached int
	if err := set.Get("key", &cached); err != nil {
		t.Skip("the last write was a delete")
	}
	if digest, _ := recordedDigest(key); digest != digestOf(t, cached) {
		t.Errorf("the value cached, %d, is not the one recorded last", cached)
	}
}
