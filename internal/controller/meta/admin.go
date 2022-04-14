package meta

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/constant"
	"github.com/penguin-statistics/backend-next/internal/core/admin"
	"github.com/penguin-statistics/backend-next/internal/core/item"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	"github.com/penguin-statistics/backend-next/internal/model/gamedata"
	"github.com/penguin-statistics/backend-next/internal/model/types"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/util/rekuest"
)

const TimeLayout = "2006-01-02 15:04:05 -07:00"

type AdminController struct {
	fx.In

	AdminService         *admin.Service
	ItemService          *item.Service
	DropMatrixService    *service.DropMatrix
	PatternMatrixService *service.PatternMatrix
	TrendService         *service.Trend
	SiteStatsService     *service.SiteStats
}

func RegisterAdmin(admin *svr.Admin, c AdminController) {
	admin.Post("/save", c.SaveRenderedObjects)
	admin.Post("/purge", c.PurgeCache)

	admin.Get("/cli/gamedata/seed", c.GetCliGameDataSeed)

	admin.Get("/refresh/matrix/:server", c.RefreshAllDropMatrixElements)
	admin.Get("/refresh/pattern/:server", c.RefreshAllPatternMatrixElements)
	admin.Get("/refresh/trend/:server", c.RefreshAllTrendElements)
	admin.Get("/refresh/sitestats/:server", c.RefreshAllSiteStats)
}

type CliGameDataSeedResponse struct {
	Items []*item.Model `json:"items"`
}

func (c AdminController) GetCliGameDataSeed(ctx *fiber.Ctx) error {
	items, err := c.ItemService.GetItems(ctx.Context())
	if err != nil {
		return err
	}

	return ctx.JSON(CliGameDataSeedResponse{
		Items: items,
	})
}

func (c *AdminController) SaveRenderedObjects(ctx *fiber.Ctx) error {
	var request gamedata.RenderedObjects
	if err := rekuest.ValidBody(ctx, &request); err != nil {
		return err
	}

	err := c.AdminService.SaveRenderedObjects(ctx.Context(), &request)
	if err != nil {
		return err
	}

	return ctx.JSON(request)
}

func (c *AdminController) PurgeCache(ctx *fiber.Ctx) error {
	var request types.PurgeCacheRequest
	if err := rekuest.ValidBody(ctx, &request); err != nil {
		return err
	}
	return cache.Delete(request.Name, request.Key)
}

func (c *AdminController) RefreshAllDropMatrixElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.DropMatrixService.RefreshAllDropMatrixElements(ctx.Context(), server)
}

func (c *AdminController) RefreshAllPatternMatrixElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.PatternMatrixService.RefreshAllPatternMatrixElements(ctx.Context(), server)
}

func (c *AdminController) RefreshAllTrendElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.TrendService.RefreshTrendElements(ctx.Context(), server)
}

func (c *AdminController) RefreshAllSiteStats(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	_, err := c.SiteStatsService.RefreshShimSiteStats(ctx.Context(), server)
	return err
}

func getTimeFromString(timeRange types.TimeRange) (startTime *time.Time, endTime *time.Time, err error) {
	start, err := time.Parse(TimeLayout, timeRange.StartTime)
	if err != nil {
		return nil, nil, err
	}
	end := time.UnixMilli(constant.FakeEndTimeMilli)
	if timeRange.EndTime.Valid {
		end, err = time.Parse(TimeLayout, timeRange.EndTime.String)
		if err != nil {
			return nil, nil, err
		}
	}
	return &start, &end, nil
}
