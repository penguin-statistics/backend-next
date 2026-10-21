package cache

import (
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog/log"
)

func NewSet[T any](prefix string) *Set[T] {
	return &Set[T]{
		prefix:      prefix + ":",
		c:           cache.New(cache.NoExpiration, time.Minute*10),
		locks:       make(map[string]*keyLock),
		calculating: make(map[string]bool),
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

	prefix string

	c *cache.Cache
}

func (c *Set[T]) key(key string) string {
	return c.prefix + key
}

func (c *Set[T]) Get(key string, dest *T) error {
	key = c.key(key)
	result, ok := c.c.Get(key)
	if !ok {
		if l := log.Trace(); l.Enabled() {
			l.Str("key", key).Msg("cache entry not found")
		}
		return ErrNotFound
	}

	// c holds only values of T: Set and MutexGetSet store nothing else
	*dest = result.(T)
	return nil
}

func (c *Set[T]) Set(key string, value T, expire time.Duration) {
	key = c.key(key)
	if l := log.Trace(); l.Enabled() {
		l.Str("key", key).Msg("setting value to cache")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.markWrittenLocked(key)
	c.c.Set(key, value, expire)
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
	err := c.Get(key, dest)
	if err == nil {
		return false, nil
	}
	// onwards, cache key does not exist

	return true, c.slowMutexGetSet(key, dest, valueFunc, expire)
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

func (c *Set[T]) slowMutexGetSet(key string, dest *T, valueFunc func() (*T, error), expire time.Duration) error {
	defer c.lockKey(key)()
	err := c.Get(key, dest)

	if err == nil {
		return nil
	}

	// the key's lock makes this the only calculation of the key
	c.writeMu.Lock()
	c.calculating[c.key(key)] = false
	c.writeMu.Unlock()

	value, err := valueFunc()

	// publish the value unless the key was written meanwhile (e.g. refreshed by the worker): then the value may have
	// been calculated from older data, and the newer one is returned instead, if it is still there
	c.writeMu.Lock()
	published := err == nil && !c.calculating[c.key(key)]
	delete(c.calculating, c.key(key))
	if published {
		c.c.Set(c.key(key), *value, expire)
	}
	c.writeMu.Unlock()
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("failed to get value from valueFunc() in MutexGetSet")
		return err
	}
	if !published && c.Get(key, dest) == nil {
		return nil
	}

	*dest = *value
	return nil
}

func (c *Set[T]) Delete(key string) error {
	key = c.key(key)
	if l := log.Trace(); l.Enabled() {
		l.Str("key", key).Msg("deleting value from cache")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.markWrittenLocked(key)
	c.c.Delete(key)

	return nil
}

func (c *Set[T]) Flush() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	for key := range c.calculating {
		c.calculating[key] = true
	}
	c.c.Flush()
	return nil
}
