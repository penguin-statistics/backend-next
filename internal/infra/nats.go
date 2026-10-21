package infra

import (
	"context"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/pkg/observability"
)

func natsErrorHandler(conn *nats.Conn, sub *nats.Subscription, err error) {
	observability.NatsAsyncErrors.Inc()
	ev := log.Error().
		Str("evt.name", "nats.error").
		Err(err).
		Str("conn.url", conn.ConnectedUrlRedacted())
	// connection-level errors come without a subscription
	if sub != nil {
		ev = ev.Str("sub.subject", sub.Subject)
	}
	ev.Msg("nats error")
}

// natsConnect connects to NATS and registers the connection metrics.
func natsConnect(url string) (*nats.Conn, error) {
	// keep reconnecting for as long as NATS is down: with the default of 60 attempts 2s apart the connection closes
	// for good after about 2 minutes, and every report publish fails until the pod restarts
	nc, err := nats.Connect(url, nats.PingInterval(time.Second*20), nats.ErrorHandler(natsErrorHandler), nats.MaxReconnects(-1))
	if err != nil {
		return nil, err
	}

	registerMetrics("nats", metricsRegisterer, newNatsConnCollector(nc))

	return nc, nil
}

// ensureStream creates the stream, or updates it to cfg when it exists with another configuration.
func ensureStream(js nats.JetStreamContext, cfg *nats.StreamConfig) error {
	_, err := js.AddStream(cfg)
	if errors.Is(err, nats.ErrStreamNameAlreadyInUse) {
		_, err = js.UpdateStream(cfg)
	}
	return err
}

// drainNATS drains the connection, delivering what its subscriptions hold and flushing what it publishes, and waits
// until it is closed or ctx ends. The client drains in the background and closes the connection however the drain
// went, reporting a subscription that did not drain in time or a final flush that failed only to the error handler, so
// drainNATS returns the first error the connection reports from the start of the drain until it is closed: the closed
// handler runs after the error handlers of every error reported before the close, in order.
//
// A connection lost during the drain bypasses the error handler: the client (v1.24.0) does not reconnect a draining
// connection, but records the error as its last error and closes it. So once closed, a last error other than the one
// the connection had when the drain started is returned too; an error of the connection from before the drain is not.
func drainNATS(ctx context.Context, nc *nats.Conn) error {
	var (
		mu       sync.Mutex
		drainErr error
	)
	logErr := nc.ErrorHandler()
	nc.SetErrorHandler(func(c *nats.Conn, sub *nats.Subscription, err error) {
		if logErr != nil {
			logErr(c, sub, err)
		}
		mu.Lock()
		if drainErr == nil {
			drainErr = err
		}
		mu.Unlock()
	})
	closed := make(chan struct{})
	nc.SetClosedHandler(func(*nats.Conn) { close(closed) })

	before := nc.LastError()
	if err := nc.Drain(); err != nil {
		return err
	}
	select {
	case <-closed:
		mu.Lock()
		err := drainErr
		mu.Unlock()
		if last := nc.LastError(); err == nil && last != nil && !errors.Is(last, before) {
			err = last
		}
		return errors.Wrap(err, "failed to drain the NATS connection")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func NATS(lc fx.Lifecycle, conf *appconfig.Config) (*nats.Conn, nats.JetStreamContext, error) {
	nc, err := natsConnect(conf.NatsURL)
	if err != nil {
		log.Error().Err(err).Msg("infra: nats: failed to connect to NATS")
		return nil, nil, err
	}

	js, err := nc.JetStream()
	if err != nil {
		log.Error().Err(err).Msg("infra: nats: failed to initialize NATS JetStream")
		return nil, nil, err
	}

	err = ensureStream(js, &nats.StreamConfig{
		Name: "penguin-reports",
		Subjects: []string{
			"REPORT.*",
		},
		Retention: nats.WorkQueuePolicy,
		// the stream has no limits, so nothing is discarded today; if a count or size limit (MaxMsgs, MaxBytes)
		// is ever set, refuse new reports (the publisher answers an error) rather than silently drop queued ones.
		// A MaxAge is different: it expires queued reports whatever the discard policy, so it would lose accepted ones.
		Discard:    nats.DiscardNew,
		Storage:    nats.FileStorage,
		Replicas:   1,
		Duplicates: time.Minute * 10,
	})
	if err != nil {
		// a process accepting reports it cannot queue or consume is worse than one failing to start
		log.Error().Err(err).Msg("infra: nats: failed to create or update jetstream stream")
		return nil, nil, err
	}

	// The dead-letter stream of the report tasks that the worker gave up on, kept until they are replayed or removed
	// by hand (see README.md). Limits retention with no limit: nothing in it expires or is discarded. Duplicates is
	// longer than AckWait and the longest retry delay, so a task moved there twice (its Term lost) is stored once.
	err = ensureStream(js, &nats.StreamConfig{
		Name: "penguin-reports-dead",
		Subjects: []string{
			"REPORT_DEAD.*",
		},
		Retention:  nats.LimitsPolicy,
		Storage:    nats.FileStorage,
		Replicas:   1,
		Duplicates: time.Minute * 10,
	})
	if err != nil {
		// the worker could not move a task there, and would retry it forever instead
		log.Error().Err(err).Msg("infra: nats: failed to create or update the dead-letter jetstream stream")
		return nil, nil, err
	}

	// Stop hooks run in the reverse order of their registration, so the report workers, which need the connection,
	// are stopped before it is drained. They bind to their durable, so draining their subscriptions keeps it.
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return drainNATS(ctx, nc)
		},
	})

	return nc, js, nil
}
