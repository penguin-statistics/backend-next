package meta

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cache"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/pkg/bininfo"
	"exusiai.dev/backend-next/internal/server/svr"
	"exusiai.dev/backend-next/internal/service"
	"exusiai.dev/gommon/constant"
)

type Meta struct {
	fx.In

	HealthService *service.Health
}

func RegisterMeta(meta *svr.Meta, c Meta) {
	meta.Get("/bininfo", c.BinInfo)

	meta.Get("/health", cache.New(cache.Config{
		// cache it for a second to mitigate potential DDoS
		// StoreResponseHeaders must stay off, or a hit replays another request's Access-Control-* headers
		Expiration:  time.Second,
		CacheHeader: constant.CacheHeader,
	}), c.Health)

	meta.Get("/ping", func(c *fiber.Ctx) error {
		// only allow intranet access to prevent abuse
		return c.SendString("pong")
	})
}

func (c *Meta) BinInfo(ctx *fiber.Ctx) error {
	return ctx.JSON(fiber.Map{
		"version": bininfo.Version,
		"build":   bininfo.BuildTime,
	})
}

// Health is the readiness probe. It answers 200 whenever the process serves HTTP, even while a dependency is down
// (they are shared by all replicas), and reports the state of the dependencies in the body.
func (c *Meta) Health(ctx *fiber.Ctx) error {
	return ctx.JSON(c.HealthService.Check(ctx.UserContext()))
}
