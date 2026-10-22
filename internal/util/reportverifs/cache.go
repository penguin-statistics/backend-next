package reportverifs

import (
	"context"
	"sync"
	"time"

	"github.com/pkg/errors"
)

// cacheTTL is how long the verification uses the reject rules and the drop infos it read, so that it reads them about
// once a minute rather than for every report: an edit of them is used up to cacheTTL late. Admins edit them rarely, and
// a minute already makes the reads negligible (the drop infos of a stage and server are read again for about 1% of
// the reports of a busy hour of production, 3% of a quiet one), so a longer TTL would make edits later for no
// measurable saving.
const cacheTTL = time.Minute

// expiringCacheMaxEntries bounds an expiringCache: it is cleared once it holds more. The drop infos of every stage of
// every server with a current time range are about 2,200 keys.
const expiringCacheMaxEntries = 8192

var errLoadPanicked = errors.New("cache load panicked")

type expiringEntry[V any] struct {
	value  V
	expiry time.Time
}

// expiringLoad is a load of a key, which the lookups of the key wait for meanwhile
type expiringLoad[V any] struct {
	done   chan struct{}
	value  V
	expiry time.Time
	err    error
}

// expiringWaitRetries bounds how often a get that waited for a load whose value had expired by its own now loads again
// instead: a database clock behind the process clock could otherwise keep it loading.
const expiringWaitRetries = 2

// expiringCache holds a value per key until the expiry its load gave it. Its zero value is empty and ready to use.
type expiringCache[K comparable, V any] struct {
	mu      sync.RWMutex
	entries map[K]expiringEntry[V]
	loads   map[K]*expiringLoad[V]
}

// get returns the value of key, or, if key holds none or it has expired at now, loads it with load, which returns the
// value and when it expires. A key is loaded once at a time: a get of a key being loaded waits for that load and returns
// its value or its error, unless ctx ends first, or the value expired by now (a load begun before its expiry and
// finished after it), when it loads again. An error is returned as load returned it, and not cached.
func (c *expiringCache[K, V]) get(ctx context.Context, now time.Time, key K, load func() (V, time.Time, error)) (V, error) {
	for retries := 0; ; retries++ {
		value, expired, err := c.getOnce(ctx, now, key, load)
		if !expired || retries == expiringWaitRetries {
			return value, err
		}
	}
}

// getOnce is get, but returns the value of a load it waited for even if it expired by now, reporting it as expired.
func (c *expiringCache[K, V]) getOnce(ctx context.Context, now time.Time, key K, load func() (V, time.Time, error)) (V, bool, error) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if ok && now.Before(e.expiry) {
		return e.value, false, nil
	}

	c.mu.Lock()
	if e, ok := c.entries[key]; ok && now.Before(e.expiry) {
		c.mu.Unlock()
		return e.value, false, nil
	}
	if l, ok := c.loads[key]; ok {
		c.mu.Unlock()
		select {
		case <-l.done:
			return l.value, l.err == nil && !now.Before(l.expiry), l.err
		case <-ctx.Done():
			var zero V
			return zero, false, ctx.Err()
		}
	}
	l := &expiringLoad[V]{done: make(chan struct{})}
	if c.loads == nil {
		c.loads = map[K]*expiringLoad[V]{}
	}
	c.loads[key] = l
	c.mu.Unlock()

	loaded := false
	defer func() { // also if load panics: then nothing is cached, and the waiting gets fail
		c.mu.Lock()
		delete(c.loads, key)
		if !loaded && l.err == nil {
			l.err = errLoadPanicked
		}
		if loaded {
			if c.entries == nil || len(c.entries) >= expiringCacheMaxEntries {
				c.entries = map[K]expiringEntry[V]{}
			}
			c.entries[key] = expiringEntry[V]{value: l.value, expiry: l.expiry}
		}
		c.mu.Unlock()
		close(l.done)
	}()
	l.value, l.expiry, l.err = load()
	loaded = l.err == nil
	return l.value, false, l.err
}
