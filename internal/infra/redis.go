package infra

import (
	"context"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/app/appconfig"
)

// redisBootTimeout bounds how long the boot waits for Redis. After a node recycle Redis may still be rescheduling
// or loading its RDB when the backend starts, which used to fail the fx start right away.
const redisBootTimeout = 60 * time.Second

func Redis(conf *appconfig.Config) (*redis.Client, error) {
	u, err := redis.ParseURL(conf.RedisURL)
	if err != nil {
		log.Error().Err(err).Msg("infra: redis: failed to parse redis url")
		return nil, err
	}

	// Open a Redis Client
	client := redis.NewClient(u)

	// check redis connection
	if err := pingRedis(client, redisBootTimeout); err != nil {
		log.Error().Err(err).Msg("infra: redis: failed to ping database")
		client.Close()
		return nil, err
	}

	return client, nil
}

// pingRedis pings until Redis answers, retrying with exponential backoff (500 ms to 5 s) until timeout has passed.
func pingRedis(client *redis.Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	backoff := 500 * time.Millisecond
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		err := client.Ping(ctx).Err()
		cancel()
		if err == nil {
			return nil
		}
		// an error reply other than LOADING, such as WRONGPASS or ERR for a bad DB index, is a configuration error that
		// waiting does not fix
		var redisErr redis.Error
		if errors.As(err, &redisErr) && !strings.HasPrefix(err.Error(), "LOADING ") {
			return err
		}
		if time.Now().Add(backoff).After(deadline) {
			return err
		}

		log.Warn().
			Str("evt.name", "infra.redis.retry").
			Err(err).
			Dur("backoff", backoff).
			Msg("infra: redis: failed to ping database, retrying")
		time.Sleep(backoff)

		backoff *= 2
		if backoff > 5*time.Second {
			backoff = 5 * time.Second
		}
	}
}
