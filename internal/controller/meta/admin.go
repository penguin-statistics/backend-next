package meta

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/model"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	"github.com/penguin-statistics/backend-next/internal/model/gamedata"
	"github.com/penguin-statistics/backend-next/internal/model/types"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/util/rekuest"
)

type AdminController struct {
	fx.In

	AdminService         *service.Admin
	ItemService          *service.Item
	DropMatrixService    *service.DropMatrix
	PatternMatrixService *service.PatternMatrix
	TrendService         *service.Trend
	SiteStatsService     *service.SiteStats
}

func RegisterAdmin(admin *svr.Admin, c AdminController) {
	admin.Get("/bonjour", c.Bonjour)
	admin.Post("/save", c.SaveRenderedObjects)
	admin.Post("/purge", c.PurgeCache)

	admin.Get("/cli/gamedata/seed", c.GetCliGameDataSeed)

	admin.Get("/refresh/matrix/:server", c.RefreshAllDropMatrixElements)
	admin.Get("/refresh/pattern/:server", c.RefreshAllPatternMatrixElements)
	admin.Get("/refresh/trend/:server", c.RefreshAllTrendElements)
	admin.Get("/refresh/sitestats/:server", c.RefreshAllSiteStats)
}

type CliGameDataSeedResponse struct {
	Items []*model.Item `json:"items"`
}

// Bonjour is for the admin dashboard to detect authentication status
func (c AdminController) Bonjour(ctx *fiber.Ctx) error {
	return ctx.SendStatus(http.StatusNoContent)
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
	errs := lo.Filter(
		lo.Map(request.Pairs, func(pair types.PurgeCachePair, _ int) error {
			err := cache.Delete(pair.Name, pair.Key)
			if err != nil {
				return errors.Wrapf(err, "cache [%s:%s]", pair.Name, pair.Key)
			}
			return nil
		}),
		func(v error, i int) bool {
			return v != nil
		},
	)
	if len(errs) > 0 {
		err := pgerr.New(http.StatusInternalServerError, "PURGE_CACHE_FAILED", "error occurred while purging cache")
		err.Extras = &pgerr.Extras{
			"caches": errs,
		}
	}
	return nil
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
