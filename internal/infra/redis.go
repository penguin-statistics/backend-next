package infra

import (
	"context"
	"crypto/tls"
	"net"
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

	// check redis connection
	if err := pingRedis(u, redisBootTimeout); err != nil {
		log.Error().Err(err).Msg("infra: redis: failed to ping database")
		return nil, err
	}

	// Open a Redis Client
	client := redis.NewClient(u)

	registerMetrics("redis", metricsRegisterer, newRedisPoolCollector(client))

	return client, nil
}

// pingRedis pings until Redis answers, retrying with exponential backoff (500 ms to 5 s) until timeout has passed.
//
// It pings with a client of its own that has ContextTimeoutEnabled set: go-redis ignores the deadline of the context
// otherwise, so a Redis that accepts connections but does not reply would hold each attempt for its whole read timeout
// and run past the timeout. Setting it on the client the app uses would change the timeouts of every later command.
// For rediss:// URLs it also dials TLS with the context, as the default dialer of go-redis v9.0.5 uses
// tls.DialWithDialer, which ignores it and only stops the handshake at the dial timeout.
func pingRedis(opts *redis.Options, timeout time.Duration) error {
	bootOpts := *opts
	bootOpts.ContextTimeoutEnabled = true
	if bootOpts.Dialer == nil && bootOpts.TLSConfig != nil {
		bootOpts.Dialer = func(ctx context.Context, network, addr string) (net.Conn, error) {
			// the same dialer as go-redis's default one, with the context; DialTimeout is read here, after
			// redis.NewClient has set its default
			d := &tls.Dialer{
				NetDialer: &net.Dialer{Timeout: bootOpts.DialTimeout, KeepAlive: 5 * time.Minute},
				Config:    bootOpts.TLSConfig,
			}
			return d.DialContext(ctx, network, addr)
		}
	}
	client := redis.NewClient(&bootOpts)
	defer client.Close()

	deadline := time.Now().Add(timeout)
	backoff := 500 * time.Millisecond
	for {
		// each attempt gets at most 5 s, and never more than what is left of the timeout
		attemptDeadline := time.Now().Add(5 * time.Second)
		if deadline.Before(attemptDeadline) {
			attemptDeadline = deadline
		}
		ctx, cancel := context.WithDeadline(context.Background(), attemptDeadline)
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
