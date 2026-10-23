package script_coldstore

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/urfave/cli/v2"

	"exusiai.dev/backend-next/internal/coldstore"
	"exusiai.dev/backend-next/internal/workers/archivewkr"
)

// runArchive runs the monthly archiving once, as the app does with PENGUIN_V3_COLDSTORE_ARCHIVE_ENABLED (whose
// settings it reads, whatever the flag says), holding the same Redis lock.
func runArchive(ctx *cli.Context, deps CommandDeps) error {
	conf := deps.Config
	db := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(conf.PostgresDSN), pgdriver.WithReadTimeout(5*time.Minute),
		pgdriver.WithApplicationName("penguin-coldstore-archive")))
	defer db.Close()

	if ctx.Bool("dry-run") {
		months, parts, others, err := coldstore.MonthPartitions(ctx.Context, db)
		if err != nil {
			return err
		}
		out := ctx.App.Writer
		now := time.Now()
		fmt.Fprintln(out, "month\tdue\tpartitions")
		for _, key := range months {
			names := make([]string, len(parts[key]))
			for i, p := range parts[key] {
				names[i] = p.Name
			}
			fmt.Fprintf(out, "%s\t%t\t%s\n", key, parts[key][0].Month.Over(now, conf.ColdstoreArchiveAfter), strings.Join(names, ","))
		}
		for _, o := range others {
			fmt.Fprintf(out, "not a month, never archived: %s\n", o)
		}
		return nil
	}

	a, err := archivewkr.NewArchiver(ctx.Context, conf, db)
	if err != nil {
		return err
	}
	opts, err := redis.ParseURL(conf.RedisURL)
	if err != nil {
		return errors.Wrap(err, "parse PENGUIN_V3_REDIS_URL")
	}
	client := redis.NewClient(opts)
	defer client.Close()
	_, err = archivewkr.RunLocked(ctx.Context, a, archivewkr.NewMutex(redsync.New(goredis.NewPool(client))))
	return err
}
