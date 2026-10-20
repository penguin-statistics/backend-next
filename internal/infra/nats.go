package infra

import (
	"time"

	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
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

// ensureStream creates the stream, or updates it to cfg when it exists with another configuration.
func ensureStream(js nats.JetStreamContext, cfg *nats.StreamConfig) error {
	_, err := js.AddStream(cfg)
	if errors.Is(err, nats.ErrStreamNameAlreadyInUse) {
		_, err = js.UpdateStream(cfg)
	}
	return err
}

func NATS(conf *appconfig.Config) (*nats.Conn, nats.JetStreamContext, error) {
	nc, err := nats.Connect(conf.NatsURL,
		nats.PingInterval(time.Second*20),
		// keep reconnecting however long NATS is down: by default the client gives up after 60 attempts (about 2
		// minutes), and the process then neither publishes nor consumes reports until it is restarted
		nats.MaxReconnects(-1),
		nats.ErrorHandler(natsErrorHandler),
	)
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
		Retention:  nats.WorkQueuePolicy,
		Discard:    nats.DiscardOld,
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
