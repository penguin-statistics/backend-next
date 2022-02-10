package cache

import (
	"context"
	"reflect"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/goccy/go-json"
)

func New(client *redis.Client, prefix string) Cache {
	return &redisCache{
		client: client,
		prefix: prefix,
	}
}

type redisCache struct {
	// m is a mutex for MutexGetSet for concurrent prevention
	m sync.Mutex

	client *redis.Client
	prefix string
}

func (c *redisCache) Get(key string, dest interface{}) error {
	resp, err := c.client.Get(context.Background(), c.prefix+key).Bytes()
	if err != nil {
		return ErrNoKey
	}

	return json.Unmarshal(resp, dest)
}

func (c *redisCache) Set(key string, value interface{}, expire time.Duration) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.client.Set(context.Background(), c.prefix+key, b, expire).Err()
}

// MutexGetSet gets value from cache and writes to dest, or if the key does not exists, it executes valueFunc
// to get cache value if the key still not exists when serially dispatched, sets value to cache and
// writes value to dest.
func (c *redisCache) MutexGetSet(key string, dest interface{}, valueFunc func() (interface{}, error), expire time.Duration) error {
	err := c.Get(key, dest)
	if err == nil {
		return nil
	} else if err != ErrNoKey {
		return err
	}
	// onwards, cache key does not exist

	return c.slowMutexGetSet(key, dest, valueFunc, expire)
}

func (c *redisCache) slowMutexGetSet(key string, dest interface{}, valueFunc func() (interface{}, error), expire time.Duration) error {
	c.m.Lock()
	defer c.m.Unlock()
	err := c.Get(key, dest)

	if err == nil {
		return nil
	} else if err != ErrNoKey {
		return err
	}

	value, err := valueFunc()
	if err != nil {
		return err
	}

	err = c.Set(key, value, expire)
	if err != nil {
		return err
	}

	// copy value to dest
	reflect.ValueOf(dest).Elem().Set(reflect.ValueOf(value))

	return nil
}

func (c *redisCache) Delete(key string) {
	c.client.Del(context.Background(), c.prefix+key)
}
