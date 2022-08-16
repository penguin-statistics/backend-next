package fiberstore

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
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

// Close implements fiber.Storage
func (r *Redis) Close() error {
	return r.Client.Close()
}

// Delete implements fiber.Storage
func (r *Redis) Delete(key string) error {
	return r.Client.HDel(context.Background(), r.HashKey, key).Err()
}

// Get implements fiber.Storage
func (r *Redis) Get(key string) ([]byte, error) {
	return r.Client.HGet(context.Background(), r.HashKey, key).Bytes()
}

// Reset implements fiber.Storage
func (r *Redis) Reset() error {
	return r.Client.Del(context.Background(), r.HashKey).Err()
}

// Set implements fiber.Storage
func (r *Redis) Set(key string, val []byte, exp time.Duration) error {
	return r.Client.HSet(context.Background(), r.HashKey, key, val).Err()
}
