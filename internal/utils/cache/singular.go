package cache

import (
	"context"
	"reflect"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/vmihailenco/msgpack/v5"
)

func NewSingular(client *redis.Client, key string) *Singular {
	return &Singular{
		client: client,
		key:    key,
	}
}

type Singular struct {
	// m is a mutex for MutexGetSet for concurrent prevention
	m sync.Mutex

	client *redis.Client
	key    string
}

func (c *Singular) Get(dest interface{}) error {
	resp, err := c.client.Get(context.Background(), c.key).Bytes()
	if err != nil {
		if err != redis.Nil {
			log.Error().Err(err).Str("key", c.key).Msg("failed to get value from redis")
		}
		return err
	}
	err = msgpack.Unmarshal(resp, dest)
	if err != nil {
		log.Error().Err(err).Str("key", c.key).Msg("failed to unmarshal value from msgpack from redis")
		return err
	}
	return nil
}

func (c *Singular) Set(value interface{}, expire time.Duration) error {
	b, err := msgpack.Marshal(value)
	if err != nil {
		log.Error().Err(err).Str("key", c.key).Msg("failed to marshal value with msgpack")
		return err
	}
	err = c.client.Set(context.Background(), c.key, b, expire).Err()
	if err != nil {
		log.Error().Err(err).Str("key", c.key).Msg("failed to set value to redis")
		return err
	}
	return nil
}

// MutexGetSet gets value from cache and writes to dest, or if the key does not exists, it executes valueFunc
// to get cache value if the key still not exists when serially dispatched, sets value to cache and
// writes value to dest.
func (c *Singular) MutexGetSet(dest interface{}, valueFunc func() (interface{}, error), expire time.Duration) error {
	err := c.Get(dest)
	if err == nil {
		return nil
	} else if err != redis.Nil {
		log.Error().Err(err).Str("key", c.key).Msg("failed to get value from redis in MutexGetSet")
		return err
	}
	// onwards, cache key does not exist

	return c.slowMutexGetSet(dest, valueFunc, expire)
}

func (c *Singular) slowMutexGetSet(dest interface{}, valueFunc func() (interface{}, error), expire time.Duration) error {
	c.m.Lock()
	defer c.m.Unlock()
	err := c.Get(dest)

	if err == nil {
		return nil
	} else if err != redis.Nil {
		log.Error().Err(err).Str("key", c.key).Msg("failed to get value from redis in MutexGetSet inner check")
		return err
	}

	value, err := valueFunc()
	if err != nil {
		log.Error().Err(err).Str("key", c.key).Msg("failed to get value from valueFunc() in MutexGetSet")
		return err
	}

	err = c.Set(value, expire)
	if err != nil {
		log.Error().Err(err).Str("key", c.key).Msg("failed to set value to redis in MutexGetSet")
		return err
	}

	// copy value to dest
	reflect.ValueOf(dest).Elem().Set(reflect.ValueOf(value))

	return nil
}

func (c *Singular) Delete() error {
	if err := c.client.Del(context.Background(), c.key).Err(); err != nil {
		log.Error().Err(err).Str("key", c.key).Msg("failed to delete value from redis")
		return err
	}
	return nil
}
