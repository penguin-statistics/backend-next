package cache

import (
	"reflect"
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog/log"
)

func NewSingular[T any](key string) *Singular[T] {
	return &Singular[T]{
		key: key,
		c:   cache.New(cache.NoExpiration, time.Minute*10),
	}
}

type Singular[T any] struct {
	// m is a mutex for MutexGetSet for concurrent prevention
	m sync.Mutex

	key string

	c *cache.Cache
}

func (c *Singular[T]) Get(dest *T) error {
	result, ok := c.c.Get(c.key)
	if !ok {
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

func (c *Singular[T]) Set(value T, expire time.Duration) error {
	c.c.Set(c.key, value, expire)
	return nil
}

// MutexGetSet gets value from cache and writes to dest, or if the key does not exists, it executes valueFunc
// to get cache value if the key still not exists when serially dispatched, sets value to cache and
// writes value to dest.
// The first return value means whether the value is got from cache or not. True means calculated; False means got from cache.
func (c *Singular[T]) MutexGetSet(dest *T, valueFunc func() (T, error), expire time.Duration) error {
	err := c.Get(dest)
	if err == nil {
		return nil
	}
	// onwards, cache key does not exist

	return c.slowMutexGetSet(dest, valueFunc, expire)
}

func (c *Singular[T]) slowMutexGetSet(dest *T, valueFunc func() (T, error), expire time.Duration) error {
	c.m.Lock()
	defer c.m.Unlock()
	err := c.Get(dest)

	if err == nil {
		return nil
	}

	value, err := valueFunc()
	if err != nil {
		log.Error().Err(err).Str("key", c.key).Msg("failed to get value from valueFunc() in MutexGetSet")
		return err
	}

	err = c.Set(value, expire)
	if err != nil {
		log.Error().Err(err).Str("key", c.key).Msg("failed to set value to cache in MutexGetSet")
		return err
	}

	// copy value to dest
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

func (c *Singular[T]) Delete() error {
	c.c.Flush()
	return nil
}
