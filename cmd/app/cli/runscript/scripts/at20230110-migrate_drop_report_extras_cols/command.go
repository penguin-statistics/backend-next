package script_migrate_drop_report_extras_cols

import (
	"github.com/uptrace/bun"
	"github.com/urfave/cli/v2"
	"go.uber.org/fx"
)

type CommandDeps struct {
	fx.In

	DB *bun.DB
}

func Command(depsFn func() CommandDeps) *cli.Command {
	return &cli.Command{
		Name:        "migrate_drop_report_extras_cols",
		Description: "migrate (source_name, version) columns from `drop_report_extras` table to `drop_reports` table",
		Action: func(ctx *cli.Context) error {
			return run(depsFn())
		},
	}
}
