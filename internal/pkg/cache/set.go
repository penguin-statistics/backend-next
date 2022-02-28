package cache

import (
	"context"
	"reflect"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/vmihailenco/msgpack/v5"
)

func NewSet(client *redis.Client, prefix string) *Set {
	return &Set{
		client: client,
		prefix: prefix + ":",
	}
}

type Set struct {
	// m is a mutex for MutexGetSet for concurrent prevention
	m sync.Mutex

	client *redis.Client
	prefix string
}

func (c *Set) key(key string) string {
	return c.prefix + key
}

func (c *Set) Get(key string, dest interface{}) error {
	key = c.key(key)
	resp, err := c.client.Get(context.Background(), key).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			log.Error().Err(err).Str("key", key).Msg("failed to get value from redis")
		}
		return err
	}
	err = msgpack.Unmarshal(resp, dest)
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("failed to unmarshal value from msgpack from redis")
		return err
	}
	return nil
}

func (c *Set) Set(key string, value interface{}, expire time.Duration) error {
	key = c.key(key)
	if l := log.Trace(); l.Enabled() {
		l.Str("key", key).Msg("setting value to redis")
	}
	b, err := msgpack.Marshal(value)
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("failed to marshal value with msgpack")
		return err
	}
	err = c.client.Set(context.Background(), key, b, expire).Err()
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("failed to set value to redis")
		return err
	}
	return nil
}

// MutexGetSet gets value from cache and writes to dest, or if the key does not exists, it executes valueFunc
// to get cache value if the key still not exists when serially dispatched, sets value to cache and
// writes value to dest.
// The first return value means whether the value is got from cache or not. True means calculated; False means getting from redis.
func (c *Set) MutexGetSet(key string, dest interface{}, valueFunc func() (interface{}, error), expire time.Duration) (bool, error) {
	err := c.Get(key, dest)
	if err == nil {
		return false, nil
	} else if !errors.Is(err, redis.Nil) {
		log.Error().Err(err).Str("key", key).Msg("failed to get value from redis in MutexGetSet")
		return false, err
	}
	// onwards, cache key does not exist

	return true, c.slowMutexGetSet(key, dest, valueFunc, expire)
}

func (c *Set) slowMutexGetSet(key string, dest interface{}, valueFunc func() (interface{}, error), expire time.Duration) error {
	c.m.Lock()
	defer c.m.Unlock()
	err := c.Get(key, dest)

	if err == nil {
		return nil
	} else if !errors.Is(err, redis.Nil) {
		log.Error().Err(err).Str("key", key).Msg("failed to get value from redis in MutexGetSet inner check")
		return err
	}

	value, err := valueFunc()
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("failed to get value from valueFunc() in MutexGetSet")
		return err
	}

	err = c.Set(key, value, expire)
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("failed to set value to redis in MutexGetSet")
		return err
	}

	// copy value to dest
	reflect.ValueOf(dest).Elem().Set(reflect.ValueOf(value))

	return nil
}

func (c *Set) Delete(key string) error {
	key = c.key(key)
	if err := c.client.Del(context.Background(), key).Err(); err != nil {
		log.Error().Err(err).Str("key", key).Msg("failed to delete value from redis")
		return err
	}

	return nil
}

func (c *Set) Clear() error {
	script := redis.NewScript(`local keys = redis.call('keys', ARGV[1])
		for i=1,#keys,5000 do
			redis.call('del', unpack(keys, i, math.min(i+4999, #keys)))
		end
	return keys`)
	err := script.Eval(context.Background(), c.client, []string{}, []string{c.prefix + "*"}).Err()
	if err != nil {
		log.Error().Err(err).Str("prefix", c.prefix).Msg("failed to clear cache")
		return err
	}
	return nil
}
