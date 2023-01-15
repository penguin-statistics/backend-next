package runscript

import (
	"github.com/urfave/cli/v2"
	"go.uber.org/fx"

	cliapp "exusiai.dev/backend-next/cmd/app/cli"
	script_migrate_drop_report_extras_cols "exusiai.dev/backend-next/cmd/app/cli/runscript/scripts/at20230110-migrate_drop_report_extras_cols"
)

func depsFn[T any]() func() T {
	return func() T {
		var deps T
		cliapp.Start(fx.Populate(&deps))
		return deps
	}
}

func Command() *cli.Command {
	return &cli.Command{
		Name:        "run-script",
		Description: "run maintenance go scripts",
		Subcommands: []*cli.Command{
			script_migrate_drop_report_extras_cols.Command(depsFn[script_migrate_drop_report_extras_cols.CommandDeps]()),
		},
	}
}
