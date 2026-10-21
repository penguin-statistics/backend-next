package cache

import (
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog/log"
)

func NewSingular[T any](key string) *Singular[T] {
	return &Singular[T]{
		key:     key,
		metrics: newMetrics(key),
		c:       cache.New(cache.NoExpiration, time.Minute*10),
	}
}

type Singular[T any] struct {
	// m is a mutex for MutexGetSet for concurrent prevention
	m sync.Mutex

	key     string
	metrics metrics

	c *cache.Cache
}

func (c *Singular[T]) Get(dest *T) error {
	err := c.get(dest)
	c.metrics.lookup(err == nil)
	return err
}

// get is Get without the lookup metric, for the lookups of MutexGetSet, which counts a lookup once
func (c *Singular[T]) get(dest *T) error {
	result, ok := c.c.Get(c.key)
	if !ok {
		return ErrNotFound
	}
	// c holds only values of T: Set and MutexGetSet store nothing else
	*dest = result.(T)
	return nil
}

func (c *Singular[T]) Set(value T, expire time.Duration) {
	c.metrics.write("set")
	c.c.Set(c.key, value, expire)
}

// MutexGetSet gets value from cache and writes to dest, or if the key does not exist, it executes valueFunc
// to get cache value if the key still not exists when serially dispatched, sets value to cache and
// writes value to dest.
// The first return value means whether the value is got from cache or not. True means calculated; False means got from cache.
func (c *Singular[T]) MutexGetSet(dest *T, valueFunc func() (T, error), expire time.Duration) error {
	err := c.get(dest)
	if err == nil {
		c.metrics.lookup(true)
		return nil
	}
	// onwards, cache key does not exist

	return c.slowMutexGetSet(dest, valueFunc, expire)
}

func (c *Singular[T]) slowMutexGetSet(dest *T, valueFunc func() (T, error), expire time.Duration) error {
	c.m.Lock()
	defer c.m.Unlock()
	err := c.get(dest)

	if err == nil {
		c.metrics.lookup(true)
		return nil
	}
	c.metrics.lookup(false)

	start := time.Now()
	value, err := valueFunc()
	c.metrics.calculated(start)
	if err != nil {
		log.Error().Err(err).Str("key", c.key).Msg("failed to get value from valueFunc() in MutexGetSet")
		return err
	}

	// not Set, so the writes metric counts only the explicit writes
	c.c.Set(c.key, value, expire)

	*dest = value
	return nil
}

func (c *Singular[T]) Delete() error {
	c.metrics.write("delete")
	c.c.Flush()
	return nil
}
