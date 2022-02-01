package infra

import (
	"github.com/nats-io/nats.go"

	"github.com/penguin-statistics/backend-next/internal/config"
)

func ProvideNats(config *config.Config) (*nats.Conn, error) {
	nc, err := nats.Connect(config.NatsURL)
	if err != nil {
		return nil, err
	}

	// js, err := nc.JetStream()
	// if err != nil {
	// 	return nil, err
	// }

	// _, err = js.UpdateStream(&nats.StreamConfig{
	// 	Name: "penguin-reports",
	// 	Subjects: []string{
	// 		"report.*",
	// 	},
	// 	Retention:  nats.WorkQueuePolicy,
	// 	Discard:    nats.DiscardOld,
	// 	Storage:    nats.FileStorage,
	// 	Replicas:   1,
	// 	Duplicates: time.Minute * 10,
	// })
	// MaxAckPending should equal to (worker count * worker channel buffer size)

	if err != nil {
		return nil, err
	}

	return nc, nil
}
