package meta

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cache"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/pkg/bininfo"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type Meta struct {
	fx.In

	HealthService *service.Health
}

func RegisterMeta(meta *svr.Meta, c Meta) {
	meta.Get("/bininfo", c.BinInfo)

	meta.Get("/health", cache.New(cache.Config{
		// cache it for a second to mitigate potential DDoS
		Expiration: time.Second,
	}), c.Health)
}

func (c *Meta) BinInfo(ctx *fiber.Ctx) error {
	return ctx.JSON(fiber.Map{
		"version": bininfo.Version,
		"build":   bininfo.BuildTime,
	})
}

func (c *Meta) Health(ctx *fiber.Ctx) error {
	if err := c.HealthService.Ping(ctx.UserContext()); err != nil {
		return err
	}

	return ctx.JSON(fiber.Map{
		"status": "ok",
	})
}
