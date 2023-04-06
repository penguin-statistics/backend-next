package infra

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/app/appconfig"
)

func Redis(conf *appconfig.Config) (*redis.Client, error) {
	u, err := redis.ParseURL(conf.RedisURL)
	if err != nil {
		log.Error().Err(err).Msg("infra: redis: failed to parse redis url")
		return nil, err
	}

	// Open a Redis Client
	client := redis.NewClient(u)

	// check redis connection
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	ping := client.Ping(ctx)
	if ping.Err() != nil {
		log.Error().Err(ping.Err()).Msg("infra: redis: failed to ping database")
		return nil, ping.Err()
	}

	return client, nil
}
