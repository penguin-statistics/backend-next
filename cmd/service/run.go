package service

import (
	"fmt"
	"penguin-stats-v4/internal/config"

	"github.com/gofiber/fiber/v2"
)

func run(server *fiber.App, config *config.Config) error {
	return server.Listen(fmt.Sprintf(":%d", config.Port))
}
