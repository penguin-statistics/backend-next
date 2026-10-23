package cache

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog/log"
)

func NewSet[T any](prefix string) *Set[T] {
	return &Set[T]{
		prefix:      prefix + ":",
		metrics:     newMetrics(prefix),
		c:           cache.New(cache.NoExpiration, time.Minute*10),
		locks:       make(map[string]*keyLock),
		calculating: make(map[string]bool),
		requested:   make(map[string]bool),
	}
}

type Set[T any] struct {
	// locks holds a lock per key in use, so that MutexGetSet calculates a key once at a time without making other
	// keys wait for it; guarded by locksMu
	locksMu sync.Mutex
	locks   map[string]*keyLock

	// writeMu guards the writes to c and calculating. calculating holds the keys MutexGetSet is calculating, with
	// whether they were written (or flushed) meanwhile: then the calculated value may be older than the written
	// one, and is not published.
	writeMu     sync.Mutex
	calculating map[string]bool
	// requested holds the keys MutexGetSet is calculating, for a request (not Refresh); guarded by writeMu
	requested map[string]bool

	prefix  string
	metrics metrics

	// c holds an *entry[T] per key
	c *cache.Cache
}

// entry is a cached value, with whether it was read since it was cached (see Used).
type entry[T any] struct {
	value T
	read  atomic.Bool
}

func newEntry[T any](value T, read bool) *entry[T] {
	e := &entry[T]{value: value}
	e.read.Store(read)
	return e
}

func (c *Set[T]) key(key string) string {
	return c.prefix + key
}

func (c *Set[T]) Get(key string, dest *T) error {
	err := c.get(key, dest)
	c.metrics.lookup(err == nil)
	return err
}

// get is Get without the lookup metric, for the lookups of MutexGetSet, which counts a lookup once
func (c *Set[T]) get(key string, dest *T) error {
	key = c.key(key)
	result, ok := c.c.Get(key)
	if !ok {
		if l := log.Trace(); l.Enabled() {
			l.Str("key", key).Msg("cache entry not found")
		}
		return ErrNotFound
	}

	// c holds only entries of T: Set, MutexGetSet and Refresh store nothing else
	e := result.(*entry[T])
	if !e.read.Load() { // most reads find it read already: no write then
		e.read.Store(true)
	}
	*dest = e.value
	return nil
}

// Has reports whether key is cached, without counting a lookup or a read (see Used).
func (c *Set[T]) Has(key string) bool {
	_, ok := c.c.Get(c.key(key))
	return ok
}

// Used reports whether the process serves key: whether MutexGetSet is calculating it for a request, or it is cached
// and was read (Get, MutexGetSet) since it was cached, which a value MutexGetSet calculated was, by the request it
// calculated it for. It counts no lookup and is no read. Set and Refresh cache unread values: a value only written
// (e.g. by the worker's refreshes) and never read is not used. It tells e.g. what to refresh after the data changed
// (cachesync): a key being calculated may be calculated from the data before.
func (c *Set[T]) Used(key string) bool {
	k := c.key(key)
	c.writeMu.Lock()
	requested := c.requested[k]
	c.writeMu.Unlock()
	if requested {
		return true
	}
	result, ok := c.c.Get(k)
	return ok && result.(*entry[T]).read.Load()
}

func (c *Set[T]) Set(key string, value T, expire time.Duration) {
	key = c.key(key)
	if l := log.Trace(); l.Enabled() {
		l.Str("key", key).Msg("setting value to cache")
	}
	c.metrics.write("set")
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.markWrittenLocked(key)
	c.c.Set(key, newEntry(value, false), expire)
}

func (c *Set[T]) markWrittenLocked(key string) {
	if _, ok := c.calculating[key]; ok {
		c.calculating[key] = true
	}
}

// MutexGetSet gets value from cache and writes to dest, or if the key does not exist, it executes valueFunc
// to get cache value if the key still not exists when serially dispatched, sets value to cache and
// writes value to dest.
// The first return value means whether the value is got from cache or not. True means calculated; False means got from cache.
func (c *Set[T]) MutexGetSet(key string, dest *T, valueFunc func() (*T, error), expire time.Duration) (bool, error) {
	err := c.get(key, dest)
	if err == nil {
		c.metrics.lookup(true)
		return false, nil
	}
	// onwards, cache key does not exist

	return true, c.slowMutexGetSet(key, dest, valueFunc, expire, nil)
}

// MutexGetSetPublishing is MutexGetSet, except that a calculated value is cached through publish, which is called
// with the value and must call cacheValue: cacheValue caches the value, unless the key was written meanwhile, and
// reports whether the value is the one MutexGetSet returns (it is not if a newer value is cached instead). publish
// can thus do something atomically with caching the value, such as recording when the cached value changed.
func (c *Set[T]) MutexGetSetPublishing(key string, dest *T, valueFunc func() (*T, error), expire time.Duration, publish func(value *T, cacheValue func() bool)) (bool, error) {
	err := c.get(key, dest)
	if err == nil {
		c.metrics.lookup(true)
		return false, nil
	}
	return true, c.slowMutexGetSet(key, dest, valueFunc, expire, publish)
}

// keyLock is the lock of a key, removed once no MutexGetSet holds or waits for it (keys such as account ids are
// too many to keep a lock for each).
type keyLock struct {
	sync.Mutex
	users int
}

func (c *Set[T]) lockKey(key string) func() {
	c.locksMu.Lock()
	l, ok := c.locks[key]
	if !ok {
		l = &keyLock{}
		c.locks[key] = l
	}
	l.users++
	c.locksMu.Unlock()

	l.Lock()
	return func() {
		l.Unlock()
		c.locksMu.Lock()
		if l.users--; l.users == 0 {
			delete(c.locks, key)
		}
		c.locksMu.Unlock()
	}
}

func (c *Set[T]) slowMutexGetSet(key string, dest *T, valueFunc func() (*T, error), expire time.Duration, publish func(value *T, cacheValue func() bool)) error {
	defer c.lockKey(key)()
	err := c.get(key, dest)

	if err == nil {
		c.metrics.lookup(true)
		return nil
	}
	c.metrics.lookup(false)

	// the key's lock makes this the only calculation of the key
	c.writeMu.Lock()
	c.calculating[c.key(key)] = false
	c.requested[c.key(key)] = true
	c.writeMu.Unlock()

	var called, returned bool
	defer func() { // also if valueFunc panics
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		delete(c.requested, c.key(key))
		if !called {
			delete(c.calculating, c.key(key))
		}
	}()
	start := time.Now()
	value, err := valueFunc()
	c.metrics.calculated(start)
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("failed to get value from valueFunc() in MutexGetSet")
		return err
	}

	// publish the value unless the key was written meanwhile (e.g. refreshed by the worker): then the value may have
	// been calculated from older data, and the newer one is returned instead, if it is still there. Which value is
	// returned is decided, and the newer one copied to dest, along with publishing: a later read could miss the newer
	// value (deleted meanwhile) after publish was told it is the one returned. The value cached is read: the request
	// gets it.
	cacheValue := func() bool {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		if called {
			return returned
		}
		called = true
		published := !c.calculating[c.key(key)]
		delete(c.calculating, c.key(key))
		if published {
			c.c.Set(c.key(key), newEntry(*value, true), expire)
		}
		returned = published || c.get(key, dest) != nil
		return returned
	}
	if publish != nil {
		publish(value, cacheValue)
	}
	if !cacheValue() { // if publish did not call it
		return nil
	}

	*dest = *value
	return nil
}

// Refresh recalculates the value of key with valueFunc and caches it through publish (as MutexGetSetPublishing does),
// replacing the cached value, which Get keeps returning meanwhile. It holds the key's lock, so it and a MutexGetSet
// calculating the key run one after the other: one never caches over the other a value calculated from older data.
// As MutexGetSet, it does not cache the value if the key was written, deleted or flushed meanwhile (e.g. purged after
// a recalculation of its data): the value may then be older than the data or the value written, and the key keeps
// what it was written, and is calculated on the next request if it was deleted. It reports whether it cached the
// value; publish is told whether through cacheValue, which it must call.
func (c *Set[T]) Refresh(key string, valueFunc func() (*T, error), expire time.Duration, publish func(value *T, cacheValue func() bool)) (bool, error) {
	defer c.lockKey(key)()
	k := c.key(key)
	// the key's lock makes this the only calculation of the key
	c.writeMu.Lock()
	c.calculating[k] = false
	c.writeMu.Unlock()

	var called, cached bool
	defer func() { // also if valueFunc panics
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		if !called {
			delete(c.calculating, k)
		}
	}()
	value, err := valueFunc()
	if err != nil {
		return false, err
	}

	cacheValue := func() bool {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		if called {
			return cached
		}
		called = true
		cached = !c.calculating[k]
		delete(c.calculating, k)
		if cached {
			c.c.Set(k, newEntry(*value, false), expire)
		}
		return cached
	}
	if publish != nil {
		publish(value, cacheValue)
	}
	return cacheValue(), nil
}

func (c *Set[T]) Delete(key string) error {
	key = c.key(key)
	if l := log.Trace(); l.Enabled() {
		l.Str("key", key).Msg("deleting value from cache")
	}
	c.metrics.write("delete")
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.markWrittenLocked(key)
	c.c.Delete(key)

	return nil
}

func (c *Set[T]) Flush() error {
	c.metrics.write("flush")
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	for key := range c.calculating {
		c.calculating[key] = true
	}
	c.c.Flush()
	return nil
}
