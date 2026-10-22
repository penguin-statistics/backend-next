// Package script_coldstore exports, verifies and indexes the cold storage of the drop reports (#752). See
// internal/coldstore for the layout of the files.
package script_coldstore

import (
	"github.com/urfave/cli/v2"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/coldstore"
)

// CommandDeps is what the commands need. They only read the configuration (the PostgreSQL DSN and the extension
// directory) instead of starting the app, so that they run on any machine that reaches the database, without Redis
// or NATS.
type CommandDeps struct {
	Config *appconfig.Config
}

func duckdbFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:  "work-dir",
			Usage: "directory for the per-job DuckDB database and spill files (a month's export holds the month's rows); default: the system's temp directory",
		},
		&cli.IntFlag{
			Name:  "threads",
			Usage: "DuckDB threads",
			Value: 4,
		},
		&cli.StringFlag{
			Name:  "memory-limit",
			Usage: "DuckDB memory limit",
			Value: "4GB",
		},
	}
}

func monthFlag(usage string) cli.Flag {
	return &cli.StringFlag{
		Name:    "month",
		Aliases: []string{"m"},
		Usage:   usage + ": YYYY-MM (UTC), or 'pre' for the reports created before 2019-05",
	}
}

func Commands(depsFn func() CommandDeps) []*cli.Command {
	return []*cli.Command{
		{
			Name:        "coldstore-export",
			Description: "export one month of drop reports from PostgreSQL to the cold storage files and record them in the directory's manifest.json",
			Flags: append([]cli.Flag{
				monthFlag("month to export"),
				&cli.BoolFlag{
					Name:  "dims",
					Usage: "also (or, without --month, only) export the drop pattern elements",
				},
				&cli.StringFlag{
					Name:     "out",
					Aliases:  []string{"o"},
					Usage:    "cold storage directory",
					Required: true,
				},
				&cli.BoolFlag{
					Name: "replace",
					Usage: "export the month again although it is in the manifest, replacing its files (e.g. after it changed in PostgreSQL); " +
						"only while PostgreSQL still holds the whole month: refused if PostgreSQL has fewer reports of it than the archive",
				},
				&cli.BoolFlag{
					Name:  "allow-fewer-rows",
					Usage: "with --replace, replace the month even if PostgreSQL has fewer reports of it than the archive",
				},
				&cli.DurationFlag{
					Name: "grace",
					Usage: "time after the end of the month before it can be exported: reports are dated when they are accepted, " +
						"so a month still gains reports while the report queue drains",
					Value: coldstore.DefaultGrace,
				},
			}, duckdbFlags()...),
			Action: func(ctx *cli.Context) error {
				return runExport(ctx, depsFn(), ctx.String("month"), ctx.Bool("dims"), ctx.String("out"))
			},
		},
		{
			Name:        "coldstore-verify",
			Description: "verify one month of the cold storage against its manifest and, unless --no-source, against PostgreSQL (full-column content hashes)",
			Flags: append([]cli.Flag{
				monthFlag("month to verify"),
				&cli.BoolFlag{
					Name:  "dims",
					Usage: "also (or, without --month, only) verify the drop pattern elements",
				},
				&cli.StringFlag{
					Name:     "dir",
					Aliases:  []string{"d"},
					Usage:    "cold storage directory",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "manifest",
					Usage: "manifest file; default: manifest.json in --dir",
				},
				&cli.BoolFlag{
					Name:  "no-source",
					Usage: "do not compare with PostgreSQL, only with the manifest",
				},
			}, duckdbFlags()...),
			Action: func(ctx *cli.Context) error {
				return runVerify(ctx, depsFn(), ctx.String("month"), ctx.Bool("dims"), ctx.String("dir"), ctx.String("manifest"), !ctx.Bool("no-source"))
			},
		},
		{
			Name: "coldstore-manifest",
			Description: "build the manifest of an existing cold storage directory from its files, without writing to the files; " +
				"replacing the directory's own manifest.json needs --force, and locks out exports and patches while the files are indexed",
			Flags: append([]cli.Flag{
				&cli.StringFlag{
					Name:     "dir",
					Aliases:  []string{"d"},
					Usage:    "cold storage directory",
					Required: true,
				},
				&cli.StringFlag{
					Name:    "out",
					Aliases: []string{"o"},
					Usage:   "manifest file to write; default: manifest.json in --dir. Paths in it are relative to --dir",
				},
				&cli.BoolFlag{
					Name:  "force",
					Usage: "replace the directory's existing manifest.json (its entries are lost: the new ones are built from the files)",
				},
			}, duckdbFlags()...),
			Action: func(ctx *cli.Context) error {
				return runManifest(ctx, depsFn(), ctx.String("dir"), ctx.String("out"), ctx.Bool("force"))
			},
		},
	}
}
