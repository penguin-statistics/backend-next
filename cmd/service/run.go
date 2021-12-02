package service

import (
	"fmt"

	"github.com/penguin-statistics/backend-next/internal/config"

	// "github.com/davecgh/go-spew/spew"
	"github.com/gofiber/fiber/v2"
)

func run(app *fiber.App, config *config.Config) error {
	return app.Listen(fmt.Sprintf(":%d", config.Port))
}
