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

	return nc, nil
}
