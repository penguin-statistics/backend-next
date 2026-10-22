package infra

import (
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/coldstore"
)

func Module() fx.Option {
	return fx.Module("infra", fx.Provide(
		NATS,
		Redis,
		RedSync,
		Postgres,
		GeoIPDatabase,
		Coldstore,
	), fx.Invoke(
		Datadog,
		// open the cold storage at startup even before anything queries it, so that missing files fail the startup
		func(*coldstore.Reader) {},
	))
}
