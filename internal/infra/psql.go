package infra

import (
	"context"
	"database/sql"
	"runtime"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/extra/bundebug"
	"github.com/uptrace/bun/extra/bunotel"

	"github.com/penguin-statistics/backend-next/internal/config"
)

func Postgres(config *config.Config) (*bun.DB, error) {
	// Open a Postgres database.
	pgdb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(config.PostgresDSN), pgdriver.WithApplicationName("penguin-backend")))

	// Create a Bun db on top of it.
	db := bun.NewDB(pgdb, pgdialect.New())
	if config.DevMode {
		db.AddQueryHook(bundebug.NewQueryHook(bundebug.WithEnabled(true), bundebug.WithVerbose(config.BunDebugVerbose)))
		db.AddQueryHook(bunotel.NewQueryHook(bunotel.WithDBName("penguin_structured")))
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}

	pgdb.SetMaxOpenConns(runtime.NumCPU() * 16)
	pgdb.SetMaxIdleConns(runtime.NumCPU() * 4)
	pgdb.SetConnMaxIdleTime(time.Minute * 10)

	return db, nil
}
