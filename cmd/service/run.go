package main

import (
	"fmt"
	"penguin-stats-v4/internal/config"

	// "github.com/davecgh/go-spew/spew"
	"github.com/gofiber/fiber/v2"
)

func run(app *fiber.App, config *config.Config) error {
	return app.Listen(fmt.Sprintf(":%d", config.Port))
}
