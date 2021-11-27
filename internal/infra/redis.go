package infra

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
)

func ProvideRedis() (*redis.Client, error) {
	// Open a Redis Client
	client := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       1, // avoid potential collision with Penguin v1 Backend
	})

	// check redis connection
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	ping := client.Ping(ctx)
	if ping.Err() != nil {
		return nil, ping.Err()
	}

	return client, nil
}
