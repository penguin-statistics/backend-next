package infra

import (
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/config"
)

func NATS(conf *config.Config) (*nats.Conn, nats.JetStreamContext, error) {
	errorHandler := func(conn *nats.Conn, sub *nats.Subscription, err error) {
		log.Error().
			Str("evt.name", "nats.error").
			Err(err).
			Str("conn.url", conn.ConnectedUrlRedacted()).
			Str("sub.subject", sub.Subject).
			Msg("nats error")
	}

	nc, err := nats.Connect(conf.NatsURL, nats.PingInterval(time.Second*20), nats.ErrorHandler(errorHandler))
	if err != nil {
		log.Error().Err(err).Msg("infra: nats: failed to connect to NATS")
		return nil, nil, err
	}

	js, err := nc.JetStream(nats.PublishAsyncMaxPending(128))
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
