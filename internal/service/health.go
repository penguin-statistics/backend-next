package service

import (
	"context"

	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"github.com/uptrace/bun"
)

var (
	ErrDatabaseNotReachable = errors.New("database not reachable")
	ErrRedisNotReachable    = errors.New("redis not reachable")
	ErrNATSNotReachable     = errors.New("nats not reachable")
)

type Health struct {
	DB    *bun.DB
	Redis *redis.Client
	NATS  *nats.Conn
}

func NewHealth(db *bun.DB, redis *redis.Client, nats *nats.Conn) *Health {
	return &Health{
		DB:    db,
		Redis: redis,
		NATS:  nats,
	}
}

func (s *Health) Ping(ctx context.Context) error {
	if err := s.DB.PingContext(ctx); err != nil {
		return errors.Wrap(ErrDatabaseNotReachable, err.Error())
	}

	if err := s.Redis.Ping(ctx).Err(); err != nil {
		return errors.Wrap(ErrRedisNotReachable, err.Error())
	}

	// nats does automatic ping for 20 seconds interval (configurated at infra/nats.go)
	status := s.NATS.Status()
	if status != nats.CONNECTED && status != nats.DRAINING_PUBS && status != nats.DRAINING_SUBS {
		return errors.Wrap(ErrNATSNotReachable, status.String())
	}

	return nil
}
