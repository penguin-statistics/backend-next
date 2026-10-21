package infra

import (
	"time"

	"github.com/nats-io/nats.go"
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

	_, err = js.AddStream(&nats.StreamConfig{
		Name: "penguin-reports",
		Subjects: []string{
			"REPORT.*",
		},
		Retention:  nats.WorkQueuePolicy,
		Discard:    nats.DiscardOld,
		Storage:    nats.FileStorage,
		Replicas:   1,
		Duplicates: time.Minute * 10,
	})

	// MaxAckPending should equal to (worker count * worker channel buffer size)

	if err != nil {
		log.Warn().Err(err).Msg("infra: nats: failed to create jetstream stream: is it already created?")
	}

	return nc, js, nil
}
