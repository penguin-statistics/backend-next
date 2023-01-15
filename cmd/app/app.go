package app

import (
	"os"

	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"

	"exusiai.dev/backend-next/cmd/app/cli/runscript"
	"exusiai.dev/backend-next/cmd/app/server"
	"exusiai.dev/backend-next/internal/pkg/bininfo"
)

func Run() {
	app := &cli.App{
		Name:        "pgbackend",
		Description: "The refactored Penguin Statistics v3 Backend. Built with Go, fiber, bun and go.uber.org/fx. Uses NATS as MQ and Redis as state synchronization.",
		Version:     bininfo.Version,
		Commands: []*cli.Command{
			server.Command(),
			runscript.Command(),
		},
	}
	if err := app.Run(os.Args); err != nil {
		log.Fatal().Err(err).Msg("failed to run app")
	}
}
