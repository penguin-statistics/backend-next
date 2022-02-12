package controllers

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type TestController struct {
	fx.In
	DropMatrixService    *service.DropMatrixService
	DropInfoService      *service.DropInfoService
	PatternMatrixService *service.PatternMatrixService
	TrendService         *service.TrendService
}

func RegisterTestController(v3 *server.V3, c TestController) {
	v3.Get("/refresh/matrix/:server", c.RefreshAllDropMatrixElements)
	v3.Get("/refresh/pattern/:server", c.RefreshAllPatternMatrixElements)
	v3.Get("/refresh/trend/:server", c.RefreshAllTrendElements)

	v3.Get("/purge/matrix/:server", func(*fiber.Ctx) error {
		return cache.ItemFromId.Clear()
	})
}

func (c *TestController) RefreshAllDropMatrixElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.DropMatrixService.RefreshAllDropMatrixElements(ctx, server)
}

func (c *TestController) RefreshAllPatternMatrixElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.PatternMatrixService.RefreshAllPatternMatrixElements(ctx, server)
}

func (c *TestController) RefreshAllTrendElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.TrendService.RefreshTrendElements(ctx, server)
}
