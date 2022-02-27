package controllers

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type ShortURLController struct {
	fx.In

	ShortURLService *service.ShortURLService
}

func RegisterShortURLController(v2 *server.V2, c ShortURLController) {
	v2.Get("/short", c.ResolveShortURL)
	v2.Get("/short/:word", c.ResolveShortURL)
}

func (c *ShortURLController) ResolveShortURL(ctx *fiber.Ctx) error {
	word := ctx.Params("word")
	return ctx.Redirect(c.ShortURLService.ResolveShortURL(ctx, word))
}
