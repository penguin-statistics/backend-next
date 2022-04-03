package v2

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type ShortURL struct {
	fx.In

	ShortURLService *service.ShortURLService
}

func RegisterShortURL(v2 *svr.V2, c ShortURL) {
	v2.Get("/short", c.Resolve)
	v2.Get("/short/:word", c.Resolve)
}

func (c *ShortURL) Resolve(ctx *fiber.Ctx) error {
	word := ctx.Params("word")
	return ctx.Redirect(c.ShortURLService.Resolve(ctx, word))
}
