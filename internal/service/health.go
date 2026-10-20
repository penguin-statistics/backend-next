package service

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
)

// healthCheckTimeout bounds the dependency checks, so the health response stays within the 1 s timeout of the
// readiness probe even while a dependency hangs.
const healthCheckTimeout = 500 * time.Millisecond

const (
	HealthOK          = "ok"
	HealthDegraded    = "degraded"
	HealthUnreachable = "unreachable"
)

// HealthStatus is the state of the dependencies: "degraded" when any of them is down.
type HealthStatus struct {
	Status   string `json:"status"`
	Postgres string `json:"postgres"`
	Redis    string `json:"redis"`
	// NATS is the connection status, e.g. CONNECTED or RECONNECTING
	NATS string `json:"nats"`
}

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

func (s *Health) Check(ctx context.Context) *HealthStatus {
	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()

	dbResult := make(chan error, 1)
	redisResult := make(chan error, 1)
	go func() { dbResult <- s.DB.PingContext(ctx) }()
	go func() { redisResult <- s.Redis.Ping(ctx).Err() }()
	dbErr := waitForCheck(ctx, dbResult)
	redisErr := waitForCheck(ctx, redisResult)

	natsStatus := s.NATS.Status()

	status := &HealthStatus{
		Status:   HealthOK,
		Postgres: HealthOK,
		Redis:    HealthOK,
		NATS:     natsStatus.String(),
	}

	if dbErr != nil {
		status.Status = HealthDegraded
		status.Postgres = HealthUnreachable
		log.Warn().Str("evt.name", "health.degraded").Err(dbErr).Msg("database not reachable")
	}

	if redisErr != nil {
		status.Status = HealthDegraded
		status.Redis = HealthUnreachable
		log.Warn().Str("evt.name", "health.degraded").Err(redisErr).Msg("redis not reachable")
	}

	// nats does automatic ping for 20 seconds interval (configurated at infra/nats.go)
	if natsStatus != nats.CONNECTED && natsStatus != nats.DRAINING_PUBS && natsStatus != nats.DRAINING_SUBS {
		status.Status = HealthDegraded
		log.Warn().Str("evt.name", "health.degraded").Str("nats.status", natsStatus.String()).Msg("nats not reachable")
	}

	return status
}

// waitForCheck returns the result of a check, or the error of ctx once it is done: go-redis ignores the deadline of
// ctx unless ContextTimeoutEnabled is set, so its ping can take the whole ReadTimeout (3 s by default).
func waitForCheck(ctx context.Context, result <-chan error) error {
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		// a check that finished while an earlier one used up the timeout still has its result: select picks between
		// ready cases at random, so take it here instead of reporting the healthy dependency as unreachable
		select {
		case err := <-result:
			return err
		default:
			return ctx.Err()
		}
	}
}
