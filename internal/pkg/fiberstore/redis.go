package fiberstore

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

type Redis struct {
	Client  *redis.Client
	HashKey string
}

// Redis implements fiber.Storage
var _ fiber.Storage = &Redis{}

func NewRedis(client *redis.Client, hashKey string) *Redis {
	return &Redis{
		Client:  client,
		HashKey: hashKey,
	}
}

func (r *Redis) key(key string) string {
	return r.HashKey + ":" + key
}

// Close implements fiber.Storage
func (r *Redis) Close() error {
	panic("fiber.Storage users should not call Close on Redis")
}

// Delete implements fiber.Storage
func (r *Redis) Delete(key string) error {
	return r.Client.Del(context.Background(), r.key(key)).Err()
}

// Get implements fiber.Storage
func (r *Redis) Get(key string) ([]byte, error) {
	return r.Client.Get(context.Background(), r.key(key)).Bytes()
}

// Reset implements fiber.Storage
func (r *Redis) Reset() error {
	panic("fiber.Storage users should not call Reset on Redis")
}

// Set implements fiber.Storage
func (r *Redis) Set(key string, val []byte, exp time.Duration) error {
	return r.Client.Set(context.Background(), r.key(key), val, exp).Err()
}
