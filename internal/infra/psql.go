package infra

import (
	"context"
	"database/sql"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/extra/bundebug"
	"github.com/uptrace/bun/extra/bunotel"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/pkg/observability"
)

// postgresReadTimeout is the socket read timeout of a DSN that sets none (no timeout or read_timeout parameter), in
// place of the 10 seconds of pgdriver. The statement timeout of advanced queries relies on it being longer than
// theirs (60 seconds): a statement the driver stops waiting for keeps running on the server.
const postgresReadTimeout = 5 * time.Minute

// postgresConnector returns the connector of dsn. A timeout set by dsn takes precedence over postgresReadTimeout.
func postgresConnector(dsn string) *pgdriver.Connector {
	return pgdriver.NewConnector(pgdriver.WithReadTimeout(postgresReadTimeout), pgdriver.WithDSN(dsn), pgdriver.WithApplicationName("penguin-backend"))
}

func Postgres(conf *appconfig.Config) (*bun.DB, error) {
	// Open a Postgres database.
	pgdb := sql.OpenDB(postgresConnector(conf.PostgresDSN))

	// Create a Bun db on top of it.
	db := bun.NewDB(pgdb, pgdialect.New())
	if conf.DevMode {
		db.AddQueryHook(bundebug.NewQueryHook(bundebug.WithEnabled(true), bundebug.WithVerbose(conf.BunDebugVerbose), bundebug.WithWriter(log.Logger)))
	}
	if conf.TracingEnabled {
		db.AddQueryHook(bunotel.NewQueryHook(bunotel.WithDBName("penguin-postgres"), bunotel.WithAttributes(semconv.DBSystemPostgreSQL)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.Error().Err(err).Msg("infra: postgres: failed to ping database")
		return nil, err
	}

	pgdb.SetMaxOpenConns(conf.PostgresMaxOpenConns)
	pgdb.SetMaxIdleConns(conf.PostgresMaxIdleConns)
	pgdb.SetConnMaxLifetime(conf.PostgresConnMaxLifeTime)
	pgdb.SetConnMaxIdleTime(conf.PostgresConnMaxIdleTime)

	// export the pool stats (open, in use, idle, waits, closed by limit) as penguinbackend_go_sql_*
	registerMetrics("postgres",
		prometheus.WrapRegistererWithPrefix(observability.ServiceName+"_", metricsRegisterer),
		collectors.NewDBStatsCollector(pgdb, "penguin-postgres"))

	return db, nil
}
