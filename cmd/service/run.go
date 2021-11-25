package service

import (
	"fmt"
	"penguin-stats-v4/internal/config"

	// "github.com/davecgh/go-spew/spew"
	"github.com/gofiber/fiber/v2"
	"github.com/uptrace/bun"
)

func run(app *fiber.App, config *config.Config, db *bun.DB) error {
	err := db.Ping()
	if err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	return app.Listen(fmt.Sprintf(":%d", config.Port))
}
