package cache

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/go-redis/redis/v8"
)

var (
	ErrNoKey = errors.New("no such key")
)

type Cache interface {
	Get(key string, dest interface{}) error
	Set(key string, value interface{}) error
	Delete(key string)
}

func New(client *redis.Client, prefix string) Cache {
	return &redisCache{client: client, prefix: prefix}
}

type redisCache struct {
	client *redis.Client
	prefix string
}

func (rbc *redisCache) Get(key string, dest interface{}) error {
	resp, err := rbc.client.Get(context.Background(), rbc.prefix+key).Bytes()
	if err != nil {
		return ErrNoKey
	}

	return json.Unmarshal(resp, dest)
}

func (rbc *redisCache) Set(key string, value interface{}) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return rbc.client.Set(context.Background(), rbc.prefix+key, b, time.Minute).Err()
}

func (rbc *redisCache) Delete(key string) {
	rbc.client.Del(context.Background(), rbc.prefix+key)
}
