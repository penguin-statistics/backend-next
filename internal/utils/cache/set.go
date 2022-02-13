package cache

import (
	"context"
	"reflect"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
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
	resp, err := c.client.Get(context.Background(), c.key(key)).Bytes()
	if err != nil {
		return err
	}
	return msgpack.Unmarshal(resp, dest)
}

func (c *Set) Set(key string, value interface{}, expire time.Duration) error {
	b, err := msgpack.Marshal(value)
	if err != nil {
		return err
	}
	return c.client.Set(context.Background(), c.key(key), b, expire).Err()
}

// MutexGetSet gets value from cache and writes to dest, or if the key does not exists, it executes valueFunc
// to get cache value if the key still not exists when serially dispatched, sets value to cache and
// writes value to dest.
func (c *Set) MutexGetSet(key string, dest interface{}, valueFunc func() (interface{}, error), expire time.Duration) error {
	err := c.Get(key, dest)
	if err == nil {
		return nil
	} else if err != redis.Nil {
		return err
	}
	// onwards, cache key does not exist

	return c.slowMutexGetSet(key, dest, valueFunc, expire)
}

func (c *Set) slowMutexGetSet(key string, dest interface{}, valueFunc func() (interface{}, error), expire time.Duration) error {
	c.m.Lock()
	defer c.m.Unlock()
	err := c.Get(key, dest)

	if err == nil {
		return nil
	} else if err != redis.Nil {
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

func (c *Set) Delete(key string) error {
	return c.client.Del(context.Background(), c.key(key)).Err()
}

func (c *Set) Clear() error {
	script := redis.NewScript(`local keys = redis.call('keys', ARGV[1])
		for i=1,#keys,5000 do
			redis.call('del', unpack(keys, i, math.min(i+4999, #keys)))
		end
	return keys`)
	return script.Eval(context.Background(), c.client, []string{}, []string{c.prefix + "*"}).Err()
}
