package script_archive_drop_reports

import (
	"github.com/urfave/cli/v2"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/service"
)

type CommandDeps struct {
	fx.In

	ArchiveService *service.Archive
}

func Command(depsFn func() CommandDeps) *cli.Command {
	return &cli.Command{
		Name:        "archive_drop_reports",
		Description: "archive one day's drop reports to S3",
		Action: func(ctx *cli.Context) error {
			return run(depsFn())
		},
	}
}
