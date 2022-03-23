package controllers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cache"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type HealthController struct {
	fx.In

	HealthService *service.HealthService
}

func RegisterHealthController(meta *svr.Meta, c HealthController) {
	meta.Get("/health", cache.New(cache.Config{
		// cache it for a second to mitigate potential DDoS
		Expiration: time.Second,
	}), c.Health)
}

func (c HealthController) Health(ctx *fiber.Ctx) error {
	return c.HealthService.Ping(ctx.Context())
}
