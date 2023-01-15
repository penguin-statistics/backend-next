package server

import "github.com/urfave/cli/v2"

func Command() *cli.Command {
	return &cli.Command{
		Name:  "start",
		Usage: "start server",
		Action: func(c *cli.Context) error {
			Run()
			return nil
		},
	}
}
