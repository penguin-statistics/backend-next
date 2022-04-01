package cache

import (
	"reflect"
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog/log"
)

func NewSet[T any](prefix string) *Set[T] {
	return &Set[T]{
		prefix: prefix + ":",
		c:      cache.New(cache.NoExpiration, time.Minute*10),
	}
}

type Set[T any] struct {
	// m is a mutex for MutexGetSet for concurrent prevention
	m sync.Mutex

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

	// copy value to dest
	var r reflect.Value
	if reflect.ValueOf(result).Kind() == reflect.Ptr {
		r = reflect.ValueOf(result).Elem()
	} else {
		r = reflect.ValueOf(result)
	}
	reflect.ValueOf(dest).Elem().Set(r)
	return nil
}

func (c *Set[T]) Set(key string, value T, expire time.Duration) error {
	key = c.key(key)
	if l := log.Trace(); l.Enabled() {
		l.Str("key", key).Msg("setting value to cache")
	}
	c.c.Set(key, value, expire)
	return nil
}

// MutexGetSet gets value from cache and writes to dest, or if the key does not exists, it executes valueFunc
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

func (c *Set[T]) slowMutexGetSet(key string, dest *T, valueFunc func() (*T, error), expire time.Duration) error {
	c.m.Lock()
	defer c.m.Unlock()
	err := c.Get(key, dest)

	if err == nil {
		return nil
	}

	value, err := valueFunc()
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("failed to get value from valueFunc() in MutexGetSet")
		return err
	}

	err = c.Set(key, *value, expire)
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("failed to set value to cache in MutexGetSet")
		return err
	}

	// copy value to dest
	var r reflect.Value
	if reflect.ValueOf(value).Kind() == reflect.Ptr {
		r = reflect.ValueOf(value).Elem()
	} else {
		r = reflect.ValueOf(value)
	}
	reflect.ValueOf(dest).Elem().Set(r)

	return nil
}

func (c *Set[T]) Delete(key string) error {
	key = c.key(key)
	if l := log.Trace(); l.Enabled() {
		l.Str("key", key).Msg("deleting value from cache")
	}
	c.c.Delete(key)

	return nil
}

func (c *Set[T]) Flush() error {
	c.c.Flush()
	return nil
}
