package script_archive_drop_reports

import (
	"github.com/urfave/cli/v2"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/service"
)

type CommandDeps struct {
	fx.In

	DropReportArchiveService *service.Archive
}

func Command(depsFn func() CommandDeps) *cli.Command {
	return &cli.Command{
		Name:        "archive_drop_reports",
		Description: "archive one day's drop reports to S3",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "date",
				Aliases:  []string{"d"},
				Usage:    "date to archive in GMT+8, in format of YYYY-MM-DD",
				Required: true,
			},
		},
		Action: func(ctx *cli.Context) error {
			date := ctx.String("date")
			return run(ctx, depsFn(), date)
		},
	}
}
