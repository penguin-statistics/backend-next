package runscript

import (
	"github.com/urfave/cli/v2"
	"go.uber.org/fx"

	cliapp "exusiai.dev/backend-next/cmd/app/cli"
	script_migrate_drop_report_extras_cols "exusiai.dev/backend-next/cmd/app/cli/runscript/scripts/at20230110-migrate_drop_report_extras_cols"
	script_coldstore "exusiai.dev/backend-next/cmd/app/cli/runscript/scripts/coldstore"
	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/app/appcontext"
	"exusiai.dev/backend-next/internal/pkg/logger"
)

func depsFn[T any]() func() T {
	return func() T {
		var deps T
		cliapp.Start(fx.Populate(&deps))
		return deps
	}
}

// coldstoreDeps reads the configuration without starting the app (see script_coldstore.CommandDeps).
func coldstoreDeps() script_coldstore.CommandDeps {
	conf, err := appconfig.Parse(appcontext.Declare(appcontext.EnvCLI))
	if err != nil {
		panic(err)
	}
	logger.Configure(conf)
	return script_coldstore.CommandDeps{Config: conf}
}

func Command() *cli.Command {
	return &cli.Command{
		Name:        "run-script",
		Description: "run maintenance go scripts",
		Subcommands: append([]*cli.Command{
			script_migrate_drop_report_extras_cols.Command(depsFn[script_migrate_drop_report_extras_cols.CommandDeps]()),
		}, script_coldstore.Commands(coldstoreDeps)...),
	}
}
