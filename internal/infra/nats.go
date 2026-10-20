package infra

import (
	"time"

	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

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

func NATS(conf *appconfig.Config) (*nats.Conn, nats.JetStreamContext, error) {
	nc, err := natsConnect(conf.NatsURL)
	if err != nil {
		log.Error().Err(err).Msg("infra: nats: failed to connect to NATS")
		return nil, nil, err
	}

	js, err := nc.JetStream(nats.PublishAsyncMaxPending(2 << 11))
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
		// the stream has no limits, so nothing is discarded today; if a limit is ever set, refuse new reports
		// (the publisher answers an error) rather than silently drop queued ones
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

	return nc, js, nil
}
