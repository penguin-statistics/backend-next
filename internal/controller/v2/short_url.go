package v2

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/server/svr"
	"exusiai.dev/backend-next/internal/service"
)

type ShortURL struct {
	fx.In

	ShortURLService *service.ShortURL
}

func RegisterShortURL(v2 *svr.V2, c ShortURL) {
	v2.Get("/short", c.Resolve)
	v2.Get("/short/:word", c.Resolve)
}

func (c *ShortURL) Resolve(ctx *fiber.Ctx) error {
	word := ctx.Params("word")
	return ctx.Redirect(c.ShortURLService.Resolve(ctx, word))
}
