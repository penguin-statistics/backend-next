package shims

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type TestController struct {
	fx.In
	DropMatrixService    *service.DropMatrixService
	PatternMatrixService *service.PatternMatrixService
	TrendService         *service.TrendService
	SiteStatsService     *service.SiteStatsService
}

func RegisterTestController(v2 *server.V2, c TestController) {
	v2.Get("/refresh/matrix/:server", c.RefreshAllDropMatrixElements)
	v2.Get("/refresh/pattern/:server", c.RefreshAllPatternMatrixElements)
	v2.Get("/refresh/trend/:server", c.RefreshAllTrendElements)
	v2.Get("/refresh/sitestats/:server", c.RefreshAllSiteStats)
}

func (c *TestController) RefreshAllDropMatrixElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.DropMatrixService.RefreshAllDropMatrixElements(ctx.Context(), server)
}

func (c *TestController) RefreshAllPatternMatrixElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.PatternMatrixService.RefreshAllPatternMatrixElements(ctx.Context(), server)
}

func (c *TestController) RefreshAllTrendElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.TrendService.RefreshTrendElements(ctx.Context(), server)
}

func (c *TestController) RefreshAllSiteStats(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	_, err := c.SiteStatsService.RefreshShimSiteStats(ctx.Context(), server)
	return err
}
