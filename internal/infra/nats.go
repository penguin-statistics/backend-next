package infra

import (
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/config"
)

func NATS(config *config.Config) (*nats.Conn, nats.JetStreamContext, error) {
	nc, err := nats.Connect(config.NatsURL)
	if err != nil {
		return nil, nil, err
	}

	js, err := nc.JetStream(nats.PublishAsyncMaxPending(128))
	if err != nil {
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
		log.Warn().Err(err).Msg("failed to create jetstream stream: is it already created?")
	}

	return nc, js, nil
}
