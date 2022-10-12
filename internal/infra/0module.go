package infra

import "go.uber.org/fx"

func Module() fx.Option {
	return fx.Module("infra", fx.Provide(
		NATS,
		Redis,
		RedSync,
		Postgres,
		LiveHouse,
		GeoIPDatabase,
	))
}
