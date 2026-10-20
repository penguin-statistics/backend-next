package infra

import (
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/app/appconfig"
)

// natsErrorHandler logs the asynchronous errors of the connection. sub is nil for an error of the connection itself,
// such as a slow consumer or a permission violation.
func natsErrorHandler(conn *nats.Conn, sub *nats.Subscription, err error) {
	ev := log.Error().
		Str("evt.name", "nats.error").
		Err(err).
		Str("conn.url", conn.ConnectedUrlRedacted())
	if sub != nil {
		ev = ev.Str("sub.subject", sub.Subject)
	}
	ev.Msg("nats error")
}

func NATS(conf *appconfig.Config) (*nats.Conn, nats.JetStreamContext, error) {
	nc, err := nats.Connect(conf.NatsURL, nats.PingInterval(time.Second*20), nats.ErrorHandler(natsErrorHandler))
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
