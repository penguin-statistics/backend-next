package shims

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models/gamedata"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type TestController struct {
	fx.In
	DropMatrixService    *service.DropMatrixService
	PatternMatrixService *service.PatternMatrixService
	TrendService         *service.TrendService
	SiteStatsService     *service.SiteStatsService
	GamedataService      *service.GamedataService
}

func RegisterTestController(v2 *server.V2, c TestController) {
	v2.Get("/refresh/matrix/:server", c.RefreshAllDropMatrixElements)
	v2.Get("/refresh/pattern/:server", c.RefreshAllPatternMatrixElements)
	v2.Get("/refresh/trend/:server", c.RefreshAllTrendElements)
	v2.Get("/refresh/sitestats/:server", c.RefreshAllSiteStats)

	v2.Get("/test", c.Test)
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

func (c *TestController) Test(ctx *fiber.Ctx) error {
	server := "CN"
	startTime := time.Date(2022, time.January, 25, 16, 0, 0, 0, constants.LocMap[server])
	endTime := time.Date(2022, time.February, 8, 4, 0, 0, 0, constants.LocMap[server])
	context := &gamedata.UpdateContext{
		ArkZoneID:    "act15side_zone1",
		ZoneName:     "测试活动",
		ZoneCategory: constants.ZoneCategoryActivity,
		Server:       server,
		StartTime:    &startTime,
		EndTime:      &endTime,
	}
	renderedObjects, err := c.GamedataService.RenderObjects(ctx.Context(), context)
	if err != nil {
		return err
	}
	return ctx.JSON(renderedObjects)
}
