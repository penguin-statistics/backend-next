package controllers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cache"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/pkg/bininfo"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type MetaController struct {
	fx.In

	HealthService *service.HealthService
}

func RegisterMetaController(meta *svr.Meta, c MetaController) {
	meta.Get("/bininfo", c.BinInfo)

	meta.Get("/health", cache.New(cache.Config{
		// cache it for a second to mitigate potential DDoS
		Expiration: time.Second,
	}), c.Health)
}

func (c *MetaController) BinInfo(ctx *fiber.Ctx) error {
	return ctx.JSON(fiber.Map{
		"version": bininfo.Version,
		"build":   bininfo.BuildTime,
	})
}

func (c *MetaController) Health(ctx *fiber.Ctx) error {
	return c.HealthService.Ping(ctx.Context())
}
