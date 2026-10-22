package script_coldstore

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/urfave/cli/v2"

	"exusiai.dev/backend-next/internal/coldstore"
)

func runProdCheck(ctx *cli.Context, deps CommandDeps) error {
	dir, manifest := ctx.String("dir"), ctx.String("manifest")
	if manifest == "" {
		if dir == "" {
			return errors.New("pass --dir or --manifest")
		}
		manifest = filepath.Join(dir, coldstore.ManifestFile)
	}
	m, err := coldstore.ReadManifest(manifest)
	if err != nil {
		return err
	}
	months := ctx.StringSlice("month")
	o := coldstore.ProdCheckOptions{
		StatementTimeout: ctx.Duration("statement-timeout"),
		Pause:            ctx.Duration("pause"),
		Reliability:      ctx.Bool("reliability"),
	}

	// the driver's read timeout must outlast the statement timeout, whatever the DSN says
	db := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(deps.Config.PostgresDSN),
		pgdriver.WithReadTimeout(o.StatementTimeout+30*time.Second), pgdriver.WithApplicationName("penguin-coldstore-prodcheck")))
	defer db.Close()

	started := time.Now()
	out := ctx.App.Writer
	var mismatches []coldstore.MonthCheck
	err = coldstore.ProdCheck(ctx.Context, db, m, months, o, func(c coldstore.MonthCheck) {
		if c.Mismatch(o.Reliability) {
			mismatches = append(mismatches, c)
		}
	})
	if err != nil {
		return err
	}

	if len(mismatches) == 0 {
		log.Info().Dur("took", time.Since(started)).Msg("coldstore: every checked month matches PostgreSQL")
		return nil
	}
	fmt.Fprintln(out, "month\tmanifest_rows\tpostgres_rows\tmanifest_reliable_rows\tpostgres_reliable_rows")
	for _, c := range mismatches {
		reliable := "-"
		if o.Reliability {
			reliable = fmt.Sprint(c.SourceReliableRows)
		}
		fmt.Fprintf(out, "%s\t%d\t%d\t%d\t%s\n", c.Month, c.ManifestRows, c.SourceRows, c.ManifestReliableRows, reliable)
	}
	if dir != "" {
		// with the files at hand, narrow the mismatch down to days
		opts := options(ctx, deps)
		for _, c := range mismatches {
			entry := m.Month(c.Month)
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(coldstore.FilePath(coldstore.KindDropReports, c.Month)))); err != nil {
				continue
			}
			files, err := coldstore.FileDayCounts(ctx.Context, opts, dir, entry)
			if err != nil {
				return err
			}
			for _, d := range coldstore.DiffDays(files, c.Days, o.Reliability) {
				if o.Reliability {
					fmt.Fprintf(out, "%s\tday %s: files %d rows (%d reliable), postgres %d rows (%d reliable)\n",
						c.Month, d.Day, d.Want.Rows, d.Want.ReliableRows, d.Got.Rows, d.Got.ReliableRows)
				} else {
					fmt.Fprintf(out, "%s\tday %s: files %d rows, postgres %d rows\n", c.Month, d.Day, d.Want.Rows, d.Got.Rows)
				}
			}
		}
	}
	keys := make([]string, len(mismatches))
	for i, c := range mismatches {
		keys[i] = c.Month
	}
	return fmt.Errorf("%d months differ from PostgreSQL: %s", len(mismatches), strings.Join(keys, " "))
}
