package cache

import (
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/goccy/go-json"

	modelv2 "exusiai.dev/backend-next/internal/model/v2"
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

	SetLastModifiedTime(key, stages("1-7"), nil)
	first := lastModified()

	SetLastModifiedTime(key, stages("1-7"), nil)
	if got := lastModified(); !got.Equal(first) {
		t.Errorf("an unchanged value changed the last modified time from %v to %v", first, got)
	}

	// changes, even in the same second, are recorded in later seconds
	SetLastModifiedTime(key, stages("1-8"), nil)
	changed := lastModified()
	if !second(changed).After(second(first)) {
		t.Errorf("a changed value recorded the last modified time %v, not in a second after %v", changed, first)
	}
	SetLastModifiedTime(key, stages("1-7"), nil)
	changedBack := lastModified()
	if !second(changedBack).After(second(changed)) {
		t.Errorf("a changed value recorded the last modified time %v, not in a second after %v", changedBack, changed)
	}

	// a flushed last modified time of an unchanged value is recorded again as it was
	if err := LastModifiedTime.Flush(); err != nil {
		t.Fatal(err)
	}
	SetLastModifiedTime(key, stages("1-7"), nil)
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
